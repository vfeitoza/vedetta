package main

import (
	"flag"
	"fmt"
	"log/slog"
	"os"
	"strings"

	"github.com/rvben/vedetta/internal/auth"
	"github.com/rvben/vedetta/internal/config"
	"github.com/rvben/vedetta/internal/storage"
)

// runCreateToken implements `vedetta auth create-token`: it mints a scoped API
// token directly against the configured database and prints the one-time
// plaintext secret. This is the offline, scriptable path for provisioning a
// long-lived credential (for example a Prometheus scraper holding metrics:read)
// without a browser session or a running server. SQLite WAL mode lets it write
// while vedetta is live, so no restart is needed.
func runCreateToken(args []string) {
	// Keep stdout reserved for the secret: the auth and storage layers emit
	// slog.Info lines (for example "api token created") that would otherwise
	// corrupt `vedetta auth create-token -name x 2>/dev/null`. Pin the default
	// logger to stderr so this holds regardless of main's later SetDefault.
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo})))

	fs := flag.NewFlagSet("auth create-token", flag.ExitOnError)
	configPath := fs.String("config", "config.yml", "path to config file")
	name := fs.String("name", "", "human-readable token name (required)")
	user := fs.String("user", "", "owning username (defaults to the sole configured user)")
	scopesArg := fs.String("scopes", "metrics:read", "comma-separated scopes")
	_ = fs.Parse(args)

	if *name == "" {
		fmt.Fprintln(os.Stderr, "usage: vedetta auth create-token -name <name> [-config config.yml] [-user <user>] [-scopes metrics:read]")
		os.Exit(2)
	}

	cfg, err := config.Load(*configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "load config: %v\n", err)
		os.Exit(1)
	}
	db, err := storage.New(cfg.Storage.DBPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "open database: %v\n", err)
		os.Exit(1)
	}
	defer db.Close()

	raw, token, err := mintToken(db, cfg.Auth, cfg.API, *user, *name, splitScopes(*scopesArg))
	if err != nil {
		fmt.Fprintf(os.Stderr, "create token: %v\n", err)
		os.Exit(1)
	}

	// Diagnostics go to stderr and the secret to stdout, so
	// `vedetta auth create-token -name x 2>/dev/null` captures only the token.
	fmt.Fprintf(os.Stderr, "created token id=%d name=%q owner=%q scopes=%s prefix=%s\n",
		token.ID, token.Name, token.Username, strings.Join(token.Scopes, ","), token.TokenPrefix)
	fmt.Println(raw)
}

// mintToken creates an API token owned by an existing account and returns the
// one-time plaintext secret alongside the stored record. It mirrors server
// startup: configured users are seeded into auth_users (the runtime source of
// truth) before the owner is resolved, so an offline mint on a database that
// predates the server's first run still references a valid account.
func mintToken(db *storage.DB, authCfg config.AuthConfig, apiCfg config.APIConfig, user, name string, scopes []string) (string, *storage.APIToken, error) {
	for _, u := range authCfg.Users {
		if err := db.SeedAuthUser(u.Username, u.PasswordHash); err != nil {
			return "", nil, fmt.Errorf("seed auth user %q: %w", u.Username, err)
		}
	}
	owner, err := resolveTokenOwner(db, user)
	if err != nil {
		return "", nil, err
	}
	checker := auth.NewFromDB(authCfg, apiCfg, db)
	defer checker.Close()
	token, raw, err := checker.CreateToken(owner, name, scopes, "cli")
	if err != nil {
		return "", nil, err
	}
	return raw, token, nil
}

// resolveTokenOwner validates that user is an account in auth_users, or selects
// the sole account when user is empty. It resolves against the database rather
// than the YAML config because runtime auth is database-primary: a user created
// through the UI exists only in auth_users. It errors when there is no usable
// owner so a token is never minted against an unknown or ambiguous account.
func resolveTokenOwner(db *storage.DB, user string) (string, error) {
	users, err := db.ListAuthUsers()
	if err != nil {
		return "", fmt.Errorf("list auth users: %w", err)
	}
	if len(users) == 0 {
		return "", fmt.Errorf("no users in auth_users; cannot own a token")
	}
	if user == "" {
		if len(users) == 1 {
			return users[0].Username, nil
		}
		names := make([]string, len(users))
		for i, u := range users {
			names[i] = u.Username
		}
		return "", fmt.Errorf("multiple users configured (%s); specify -user", strings.Join(names, ", "))
	}
	for _, u := range users {
		if u.Username == user {
			return user, nil
		}
	}
	return "", fmt.Errorf("user %q not found in auth_users", user)
}

// splitScopes parses a comma-separated scope list, trimming whitespace and
// dropping empty entries.
func splitScopes(s string) []string {
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}
