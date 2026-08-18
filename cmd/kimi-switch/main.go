package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/kyui-azusa/kimi-switch/internal/config"
	"github.com/kyui-azusa/kimi-switch/internal/kimiimport"
	"github.com/kyui-azusa/kimi-switch/internal/proxy"
	"github.com/kyui-azusa/kimi-switch/internal/rotation"
	"github.com/kyui-azusa/kimi-switch/internal/secret"
)

var (
	secretReadForBackfill = secret.Read
	configSaveForBackfill = config.Save
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "kimi-switch:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		printUsage()
		return nil
	}
	path := config.Path()
	switch args[0] {
	case "init":
		return initConfig(path)
	case "serve", "start":
		return serve(path)
	case "list", "ls", "status":
		return showStatus(path)
	case "use":
		if len(args) < 2 {
			return errors.New("usage: kimi-switch use <key-name>")
		}
		manager, err := loadManager(path)
		if err != nil {
			return err
		}
		if err := manager.SetActive(args[1]); err != nil {
			return err
		}
		fmt.Println("active key:", args[1])
		return nil
	case "rotation":
		return setRotation(path, args[1:])
	case "add":
		return addKey(path, args[1:])
	case "secret-set":
		return setSecret(args[1:])
	case "import-kimi":
		return importKimi(args[1:])
	case "print-config":
		return printClientConfig(path)
	default:
		return fmt.Errorf("unknown command %q; run kimi-switch for help", args[0])
	}
}

func initConfig(path string) error {
	if _, err := os.Stat(path); err == nil {
		fmt.Println("config already exists:", path)
		return nil
	}
	c := config.Default()
	if err := config.Save(path, c); err != nil {
		return err
	}
	fmt.Println("created:", path)
	fmt.Println("add a key with: kimi-switch add work --env KIMI_WORK_KEY")
	return nil
}

func serve(path string) error {
	c, err := config.Ensure(path)
	if err != nil {
		return err
	}
	c = backfillLocalAPIKeys(path, c, func(message string) {
		fmt.Fprintln(os.Stderr, "kimi-switch:", message)
	})
	manager, err := rotation.NewManager(c, path, config.StatePath(c))
	if err != nil {
		return err
	}
	server := &http.Server{
		Addr:              c.ListenAddr,
		Handler:           proxy.NewServer(manager),
		ReadHeaderTimeout: 10 * time.Second,
	}
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-stop
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.Shutdown(ctx)
	}()
	fmt.Println("KimiSwitch listening on http://" + c.ListenAddr)
	fmt.Println("upstream:", c.UpstreamBaseURL)
	fmt.Println("rotation:", c.Rotation.Enabled, "mode:", c.Rotation.Mode)
	err = server.ListenAndServe()
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

func backfillLocalAPIKeys(path string, c config.Config, warn func(string)) config.Config {
	changed := false
	for i := range c.Keys {
		key := &c.Keys[i]
		if key.SecretRef == "" || key.APIKey != "" {
			continue
		}
		apiKey, err := secretReadForBackfill(key.SecretRef)
		if err != nil {
			if warn != nil {
				warn(fmt.Sprintf("warning: could not backfill local fallback for %q: %v", key.Name, err))
			}
			continue
		}
		key.APIKey = apiKey
		changed = true
	}
	if !changed {
		return c
	}
	if err := configSaveForBackfill(path, c); err != nil && warn != nil {
		warn(fmt.Sprintf("warning: could not persist local API-key fallbacks: %v", err))
	}
	return c
}

func loadManager(path string) (*rotation.Manager, error) {
	c, err := config.Load(path)
	if err != nil {
		return nil, err
	}
	return rotation.NewManager(c, path, config.StatePath(c))
}

func setRotation(path string, args []string) error {
	if len(args) < 1 || (args[0] != "on" && args[0] != "off") {
		return errors.New("usage: kimi-switch rotation on|off [sticky]")
	}
	mode := ""
	if len(args) > 1 {
		mode = args[1]
	}
	manager, err := loadManager(path)
	if err != nil {
		return err
	}
	enabled := args[0] == "on"
	if err := manager.SetRotation(enabled, mode); err != nil {
		return err
	}
	c := manager.Config()
	fmt.Printf("rotation: %s, mode: %s\n", boolWord(c.Rotation.Enabled), c.Rotation.Mode)
	return nil
}

func addKey(path string, args []string) error {
	if len(args) < 1 {
		return errors.New("usage: kimi-switch add <name> --env ENV_NAME | --secret-ref SERVICE")
	}
	name := args[0]
	key := config.KeyConfig{Name: name, Enabled: true}
	for i := 1; i < len(args); i++ {
		switch args[i] {
		case "--env":
			if i+1 >= len(args) {
				return errors.New("--env requires an environment variable name")
			}
			key.APIKeyEnv = args[i+1]
			i++
		case "--secret-ref":
			if i+1 >= len(args) {
				return errors.New("--secret-ref requires a keychain service name")
			}
			key.SecretRef = args[i+1]
			i++
		default:
			return fmt.Errorf("unknown add option %q", args[i])
		}
	}
	if key.APIKeyEnv == "" && key.SecretRef == "" {
		return errors.New("choose --env or --secret-ref; raw keys are intentionally not accepted on the command line")
	}
	c, err := config.Load(path)
	if err != nil {
		return err
	}
	for i := range c.Keys {
		if c.Keys[i].Name == name {
			c.Keys[i] = key
			return config.Save(path, c)
		}
	}
	c.Keys = append(c.Keys, key)
	if c.ActiveKey == "" {
		c.ActiveKey = name
	}
	if err := config.Save(path, c); err != nil {
		return err
	}
	fmt.Println("added key:", name)
	return nil
}

func setSecret(args []string) error {
	if len(args) != 1 {
		return errors.New("usage: kimi-switch secret-set <keychain-service>")
	}
	fmt.Fprint(os.Stderr, "paste API key and press Enter: ")
	value, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return err
	}
	value = strings.TrimSpace(value)
	if err := secret.Write(args[0], value); err != nil {
		return err
	}
	fmt.Println("stored keychain item:", args[0])
	return nil
}

func importKimi(args []string) error {
	path := kimiimport.DefaultPath()
	if len(args) > 0 {
		path = args[0]
	}
	providers, err := kimiimport.Parse(path)
	if err != nil {
		return err
	}
	if len(providers) == 0 {
		return fmt.Errorf("no API-key provider found in %s", path)
	}
	c, err := config.Load(config.Path())
	if err != nil {
		return err
	}
	for name, apiKey := range providers {
		service := "kimi-switch/" + name
		if err := secret.Write(service, apiKey); err != nil {
			return fmt.Errorf("store provider %q in Keychain: %w", name, err)
		}
		key := config.KeyConfig{Name: name, Account: name, SecretRef: service, APIKey: apiKey, Enabled: true}
		replaced := false
		for i := range c.Keys {
			if c.Keys[i].Name == name {
				c.Keys[i] = key
				replaced = true
				break
			}
		}
		if !replaced {
			c.Keys = append(c.Keys, key)
		}
		if c.ActiveKey == "" {
			c.ActiveKey = name
		}
		fmt.Println("imported:", name, "-> Keychain", service)
	}
	return config.Save(config.Path(), c)
}

func showStatus(path string) error {
	manager, err := loadManager(path)
	if err != nil {
		return err
	}
	c, state := manager.Snapshot()
	if len(c.Keys) == 0 {
		fmt.Println("no keys configured")
		return nil
	}
	for _, key := range c.Keys {
		stats := state.Keys[key.Name]
		cooldown := "ready"
		if !stats.CooldownUntil.IsZero() && time.Now().UTC().Before(stats.CooldownUntil) {
			cooldown = "cooldown until " + stats.CooldownUntil.Local().Format(time.RFC3339)
		}
		marker := " "
		if key.Name == c.ActiveKey {
			marker = "*"
		}
		fmt.Printf("%s %-16s requests=%-5d tokens=%-8d estimated=%-8d %s\n", marker, key.Name, stats.Requests, stats.TotalTokens, stats.EstimatedTokens, cooldown)
	}
	fmt.Printf("rotation=%s mode=%s listen=%s\n", boolWord(c.Rotation.Enabled), c.Rotation.Mode, c.ListenAddr)
	return nil
}

func printClientConfig(path string) error {
	c, err := config.Load(path)
	if err != nil {
		return err
	}
	localKey := c.LocalAPIKey
	if localKey == "" {
		localKey = config.DefaultLocalAPIKey
	}
	value := map[string]any{
		"kimi_model_name":     "kimi-for-coding",
		"kimi_model_api_key":  localKey,
		"kimi_model_base_url": "http://" + c.ListenAddr + "/v1",
		"note":                "Use this as a local proxy target; keep the upstream Kimi key inside KimiSwitch.",
	}
	b, _ := json.MarshalIndent(value, "", "  ")
	fmt.Println(string(b))
	return nil
}

func boolWord(value bool) string {
	if value {
		return "on"
	}
	return "off"
}

func printUsage() {
	fmt.Println(`KimiSwitch - local Kimi Code key pool and proxy

Commands:
  init                              create the local config
  add <name> --env ENV              add a key without putting it in the config
  add <name> --secret-ref SERVICE   use a macOS Keychain item
  secret-set SERVICE                read one key from stdin into Keychain
  import-kimi [PATH]                import API-key providers from Kimi config.toml
  use <name>                        choose the manual active key
  rotation on|off [sticky]           toggle sticky failover rotation
  list                              show key usage and cooldown state
  print-config                      print Kimi CLI local-proxy settings
  serve                             start the local proxy

The default listener is 127.0.0.1:15722. Rotation is off by default.
Sticky mode keeps using the current key and only switches on failure. Legacy request/token modes are migrated automatically.`)
}
