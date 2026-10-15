package main

import (
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/handsomedogx/codearts2api/internal/auth"
	"github.com/handsomedogx/codearts2api/internal/config"
	"github.com/handsomedogx/codearts2api/internal/securestore"
	"github.com/handsomedogx/codearts2api/internal/server"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "credentials" {
		if err := runCredentialsCommand(os.Args[2:]); err != nil {
			log.Fatal(err)
		}
		return
	}
	if len(os.Args) > 1 && (os.Args[1] == "help" || os.Args[1] == "--help" || os.Args[1] == "-h") {
		printUsage()
		return
	}

	settings := config.Load()
	addr := strings.TrimSpace(os.Getenv("CODEARTS2API_ADDR"))
	if addr == "" {
		addr = fmt.Sprintf("%s:%d", settings.Host, settings.Port)
	}

	handler, stopRuntime, err := server.NewRuntime()
	if err != nil {
		log.Fatal(err)
	}
	defer stopRuntime()

	srv := &http.Server{
		Addr:    addr,
		Handler: handler,
	}

	log.Printf("codearts2api (Go) listening on http://%s", addr)
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatal(err)
	}
}

func runCredentialsCommand(args []string) error {
	if len(args) == 0 {
		printCredentialsUsage()
		return nil
	}
	switch args[0] {
	case "export":
		fs := flag.NewFlagSet("credentials export", flag.ContinueOnError)
		fs.SetOutput(os.Stderr)
		defaultName := "codearts2api-accounts-" + time.Now().Format("20060102-150405") + ".enc"
		output := fs.String("output", defaultName, "encrypted migration bundle path")
		fs.StringVar(output, "o", defaultName, "encrypted migration bundle path")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		password, err := migrationPassword(true)
		if err != nil {
			return err
		}
		manager, err := credentialManager()
		if err != nil {
			return err
		}
		count, err := manager.ExportCredentials(*output, password)
		if err != nil {
			return err
		}
		absolute, _ := filepath.Abs(*output)
		fmt.Printf("Exported %d account(s) to %s\n", count, absolute)
		fmt.Println("Keep the migration password separately. The bundle cannot be recovered without it.")
		return nil
	case "import":
		fs := flag.NewFlagSet("credentials import", flag.ContinueOnError)
		fs.SetOutput(os.Stderr)
		input := fs.String("input", "", "encrypted migration bundle path")
		fs.StringVar(input, "i", "", "encrypted migration bundle path")
		replace := fs.Bool("replace", false, "explicitly replace accounts with matching ids")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		if strings.TrimSpace(*input) == "" {
			return fmt.Errorf("--input is required")
		}
		password, err := migrationPassword(false)
		if err != nil {
			return err
		}
		manager, err := credentialManager()
		if err != nil {
			return err
		}
		result, err := manager.ImportCredentials(*input, password, *replace)
		if err != nil {
			return err
		}
		fmt.Printf("Imported %d account(s)", result.Imported)
		if result.Replaced > 0 {
			fmt.Printf(" (%d replaced)", result.Replaced)
		}
		fmt.Println(".")
		return nil
	case "import-plain":
		fs := flag.NewFlagSet("credentials import-plain", flag.ContinueOnError)
		fs.SetOutput(os.Stderr)
		input := fs.String("input", "", "plaintext credential JSON path")
		fs.StringVar(input, "i", "", "plaintext credential JSON path")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		if strings.TrimSpace(*input) == "" {
			return fmt.Errorf("--input is required")
		}
		manager, err := credentialManager()
		if err != nil {
			return err
		}
		result, err := manager.ImportPlainCredentials(*input)
		if err != nil {
			return err
		}
		fmt.Printf("Imported %d new account(s)", result.Imported)
		if result.Updated > 0 {
			fmt.Printf(" and updated %d existing account(s)", result.Updated)
		}
		fmt.Println(".")
		fmt.Println("The source JSON is not copied or modified. Protect or delete the plaintext source file yourself when it is no longer needed.")
		return nil
	case "help", "--help", "-h":
		printCredentialsUsage()
		return nil
	default:
		return fmt.Errorf("unknown credentials command %q; expected export, import, or import-plain", args[0])
	}
}

func credentialManager() (*auth.Manager, error) {
	settings := config.Load()
	securePaths := []string{settings.GatewaySettingsFile, settings.PendingAuthFile, settings.AccountsDir}
	if settings.MigrateLegacy {
		securePaths = append(securePaths,
			filepath.Join(settings.LegacyDataDir, "settings.json"),
			filepath.Join(settings.LegacyDataDir, "oauth_pending.json"),
			filepath.Join(settings.LegacyDataDir, "accounts"),
		)
	}
	if err := securestore.PrepareRuntime(securePaths...); err != nil {
		return nil, fmt.Errorf("initialize secure credential storage: %w", err)
	}
	return auth.NewManager(settings), nil
}

func migrationPassword(confirm bool) (string, error) {
	if value := os.Getenv("CODEARTS2API_MIGRATION_PASSWORD"); value != "" {
		return value, nil
	}
	password, err := readSecretLine("Migration password: ")
	if err != nil {
		return "", fmt.Errorf("read migration password: %w", err)
	}
	if confirm {
		confirmation, err := readSecretLine("Confirm migration password: ")
		if err != nil {
			return "", fmt.Errorf("read migration password confirmation: %w", err)
		}
		if password != confirmation {
			return "", fmt.Errorf("migration passwords do not match")
		}
	}
	return password, nil
}

func printUsage() {
	fmt.Println("codearts2api")
	fmt.Println()
	fmt.Println("Usage:")
	fmt.Println("  codearts2api                         Start the local gateway")
	fmt.Println("  codearts2api credentials export     Export all accounts to a password-encrypted migration bundle")
	fmt.Println("  codearts2api credentials import     Import a migration bundle and re-encrypt for this machine")
	fmt.Println("  codearts2api credentials import-plain  Verify plaintext credentials and encrypt them for this machine")
}

func printCredentialsUsage() {
	fmt.Println("Credential migration:")
	fmt.Println("  codearts2api credentials export [-o accounts.enc]")
	fmt.Println("  codearts2api credentials import -i accounts.enc [--replace]")
	fmt.Println("  codearts2api credentials import-plain -i accounts.json")
	fmt.Println()
	fmt.Println("The password is prompted without echo. For automation, set CODEARTS2API_MIGRATION_PASSWORD.")
	fmt.Println("Import refuses matching account ids unless --replace is explicitly supplied.")
	fmt.Println("Plain import accepts one account object, an account array, or {\"accounts\":[...]}; each account requires environment, access_key_id, and secret_access_key.")
	fmt.Println("Plain credentials are verified with Huawei caller identity and then immediately protected with the local secure store.")
}
