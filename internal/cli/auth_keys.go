package cli

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"regexp"
	"strings"

	"github.com/bcdock/cli/internal/client"
	"github.com/bcdock/cli/internal/output"
	"github.com/spf13/cobra"
)

// #706: list and revoke the active company's API keys over GET / DELETE /api/v1/api-keys. Minting
// stays portal-only by design (SEC-049): a key cannot mint keys.

// apiKey mirrors ApiKeyListItemResponse. The API never returns the secret, only its prefix.
type apiKey struct {
	ID         string   `json:"id"`
	Name       string   `json:"name"`
	KeyPrefix  string   `json:"keyPrefix"`
	Scopes     []string `json:"scopes"`
	CreatedAt  string   `json:"createdAt"`
	ExpiresAt  *string  `json:"expiresAt"`
	LastUsedAt *string  `json:"lastUsedAt"`
}

type apiKeyRow struct {
	ID       string `header:"ID"`
	Name     string `header:"NAME"`
	Prefix   string `header:"PREFIX"`
	Scopes   string `header:"SCOPES"`
	Created  string `header:"CREATED"`
	LastUsed string `header:"LAST USED"`
	Expires  string `header:"EXPIRES"`
}

type apiKeyRevoked struct {
	ID      string `json:"id"      header:"ID"`
	Revoked bool   `json:"revoked" header:"REVOKED"`
}

var guidShape = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// day trims an ISO timestamp to its date; "-" when absent.
func day(ts *string) string {
	if ts == nil || *ts == "" {
		return "-"
	}
	if len(*ts) >= 10 {
		return (*ts)[:10]
	}
	return *ts
}

var authKeysCmd = &cobra.Command{
	Use:   "keys",
	Short: "List and revoke your company's API keys",
	Long: `List and revoke the API keys of your active company.

Keys are created in the portal (Profile -> API keys), or by 'bcdock auth login'.
A key cannot create keys, but any key of the company can list and revoke them,
so a leaked key can be revoked from the CLI.

Exit codes:
  0   ok
  1   general error`,
	Example: `  bcdock auth keys list
  bcdock auth keys revoke <id>`,
}

var authKeysListCmd = &cobra.Command{
	Use:   "list",
	Short: "List your company's API keys (never the secrets)",
	Long: `List the active company's API keys: id, name, the key's prefix, scopes, and
when it was created, last used and expires. The secret is never shown; it is
displayed once, when the key is created.

Exit codes:
  0   ok
  3   auth failure (missing or invalid token)
  4   rate-limited`,
	Example: `  bcdock auth keys list
  bcdock auth keys list -o json`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		r := GetResolved(cmd)

		var keys []apiKey
		if err := r.Client.Do(cmd.Context(), http.MethodGet, "/api/v1/api-keys", nil, &keys); err != nil {
			return err
		}
		if r.Printer.Format == output.FormatJSON {
			if keys == nil {
				keys = []apiKey{}
			}
			return r.Printer.Print(keys)
		}
		if len(keys) == 0 {
			r.Printer.Info("No API keys. Create one in the portal (Profile -> API keys) or with 'bcdock auth login'.")
			return nil
		}
		rows := make([]apiKeyRow, len(keys))
		for i, k := range keys {
			created := k.CreatedAt
			rows[i] = apiKeyRow{
				ID: k.ID, Name: k.Name, Prefix: k.KeyPrefix, Scopes: strings.Join(k.Scopes, ","),
				Created: day(&created), LastUsed: day(k.LastUsedAt), Expires: day(k.ExpiresAt),
			}
		}
		return r.Printer.Print(rows)
	},
}

var authKeysRevokeCmd = &cobra.Command{
	Use:   "revoke <id>",
	Short: "Revoke one of your company's API keys",
	Long: `Revoke an API key by its id (from 'bcdock auth keys list'). The key stops
working immediately for new requests. Revoking cannot be undone.

Prompts for confirmation unless --yes is passed. When stdin is not a terminal
(scripts, agents), --yes is required.

Exit codes:
  0   ok, or cancelled at the prompt
  1   general error (for example, no --yes without a terminal)
  3   auth failure (missing or invalid token)
  4   rate-limited
  5   no key with that id in your active company`,
	Example: `  bcdock auth keys list
  bcdock auth keys revoke <id>
  bcdock auth keys revoke <id> --yes -o json`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		r := GetResolved(cmd)
		id := strings.TrimSpace(args[0])
		yes, _ := cmd.Flags().GetBool("yes")

		if !guidShape.MatchString(id) {
			return fmt.Errorf("%q is not a key id: use the ID column of 'bcdock auth keys list'", id)
		}
		if !yes {
			needYes := fmt.Errorf("revoking cannot be undone: pass --yes to revoke key %s without a prompt", id)
			if !isTTY(os.Stdin) {
				return needYes
			}
			// isTTY is true for /dev/null too (a character device), which is how agents often run,
			// so an EOF at the prompt means "no terminal", not a read failure.
			answer, err := Prompt(fmt.Sprintf("Revoke API key %s? It stops working immediately. [y/N]: ", id))
			if errors.Is(err, io.EOF) {
				return needYes
			}
			if err != nil {
				return err
			}
			if !strings.EqualFold(strings.TrimSpace(answer), "y") {
				r.Printer.Info("Cancelled.")
				return nil
			}
		}

		if err := r.Client.Do(cmd.Context(), http.MethodDelete, "/api/v1/api-keys/"+id, nil, nil); err != nil {
			var apiErr *client.APIError
			if errors.As(err, &apiErr) && apiErr.Status == http.StatusNotFound {
				// The route only sees the active company's keys, so another company's key is a 404 too.
				friendly := *apiErr
				friendly.ErrorText = ""
				friendly.Message = fmt.Sprintf("No API key %s in your active company. 'bcdock auth keys list' shows its keys", id)
				return &friendly
			}
			return err
		}

		r.Printer.Info("Revoked API key %s.", id)
		return r.Printer.Print(apiKeyRevoked{ID: id, Revoked: true})
	},
}

func init() {
	authKeysRevokeCmd.Flags().Bool("yes", false, "Revoke without a confirmation prompt")
	authKeysCmd.AddCommand(authKeysListCmd, authKeysRevokeCmd)
	authCmd.AddCommand(authKeysCmd)
}
