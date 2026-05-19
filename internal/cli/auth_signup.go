package cli

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/spf13/cobra"
)

// ── Wire types ──────────────────────────────────────────────────────────────

// waitlistFullRequest mirrors POST /api/public/waitlist (full schema).
type waitlistFullRequest struct {
	Name            string `json:"name"`
	Email           string `json:"email"`
	UseCase         string `json:"useCase,omitempty"`
	BcVersion       string `json:"bcVersion,omitempty"`
	Country         string `json:"country,omitempty"`
	ArtifactType    string `json:"artifactType,omitempty"`
	MultiTenant     *bool  `json:"multiTenant,omitempty"`
	PreferredRegion string `json:"preferredRegion,omitempty"`
	SourcePage      string `json:"sourcePage,omitempty"`
}

// signupRequest mirrors POST /api/auth/signup.
type signupRequest struct {
	Email             string `json:"email"`
	InviteCode        string `json:"inviteCode,omitempty"`
	Name              string `json:"name,omitempty"`
	BcVersion         string `json:"bcVersion,omitempty"`
	Country           string `json:"country,omitempty"`
	ArtifactType      string `json:"artifactType,omitempty"`
	MultiTenant       *bool  `json:"multiTenant,omitempty"`
	Region            string `json:"region,omitempty"`
	AcceptEula        bool   `json:"acceptEula"`
	AcceptInsiderEula bool   `json:"acceptInsiderEula"`
}

type signupResponse struct {
	Ok                 bool   `json:"ok"`
	UserId             string `json:"userId"`
	EnvironmentId      string `json:"environmentId,omitempty"`
	EnvironmentShortId string `json:"environmentShortId,omitempty"`
}

// validateInviteCodeResponse mirrors POST /api/public/validate-invite-code.
// Reused for pre-fill so the user doesn't re-enter what was already on the
// waitlist entry tied to the code.
type validateInviteCodeResponse struct {
	Valid          bool                       `json:"valid"`
	Email          string                     `json:"email,omitempty"`
	WaitlistConfig *waitlistConfigPrefillData `json:"waitlistConfig,omitempty"`
}

type waitlistConfigPrefillData struct {
	Name         string `json:"name,omitempty"`
	BcVersion    string `json:"bcVersion,omitempty"`
	Country      string `json:"country,omitempty"`
	ArtifactType string `json:"artifactType,omitempty"`
	MultiTenant  *bool  `json:"multiTenant,omitempty"`
	Region       string `json:"region,omitempty"`
}

// ── bcdock auth join-waitlist ──────────────────────────────────────────────

var authJoinWaitlistCmd = &cobra.Command{
	Use:   "join-waitlist",
	Short: "Submit a waitlist entry to request access to BCDock",
	Long: `Join the BCDock waitlist with optional default environment configuration.

BCDock is currently in early access - we onboard users carefully so we can
get the experience right. After you submit, an admin reviews your entry and
emails you an invite code (typically within 48 hours). Once you have the
code, run:

  bcdock auth signup --invite-code CODE --email you@example.com

Order of prompts (skip any with --flag or by pressing enter on optional ones):
  1. Default BC config - version, country, artifact type, region (all optional)
  2. What you'd like BCDock to help you with (optional)
  3. Your name (required)
  4. Your email (required)

Exit codes:
  0   ok
  1   general error
  4   rate-limited`,
	Example: `  bcdock auth join-waitlist
  bcdock auth join-waitlist --name "Jane Smith" --email jane@example.com
  bcdock auth join-waitlist --name "Jane Smith" --email jane@example.com --bc-version 25.5 --country au --region australiaeast`,
	RunE: runJoinWaitlist,
}

func runJoinWaitlist(cmd *cobra.Command, _ []string) error {
	r := GetResolved(cmd)

	// 1. Default config (optional)
	bcVersion, _ := cmd.Flags().GetString("bc-version")
	country, _ := cmd.Flags().GetString("country")
	artifactType, _ := cmd.Flags().GetString("artifact-type")
	region, _ := cmd.Flags().GetString("region")
	multiTenantFlag, _ := cmd.Flags().GetString("multi-tenant")

	if bcVersion == "" && !cmd.Flags().Changed("bc-version") {
		v, err := promptOptional("BC version (e.g. 27.5, leave empty to skip): ")
		if err != nil {
			return err
		}
		bcVersion = v
	}
	if country == "" && !cmd.Flags().Changed("country") {
		v, err := promptOptional("Country code (e.g. AU, US, leave empty to skip): ")
		if err != nil {
			return err
		}
		country = v
	}
	if artifactType == "" && !cmd.Flags().Changed("artifact-type") {
		v, err := promptOptional("Artifact type [Sandbox/OnPrem, leave empty to skip]: ")
		if err != nil {
			return err
		}
		artifactType = v
	}
	if region == "" && !cmd.Flags().Changed("region") {
		v, err := promptOptional("Preferred region (e.g. australiaeast, leave empty to skip): ")
		if err != nil {
			return err
		}
		region = v
	}

	var multiTenant *bool
	if multiTenantFlag != "" {
		v := strings.EqualFold(multiTenantFlag, "true") || strings.EqualFold(multiTenantFlag, "yes")
		multiTenant = &v
	}

	// 2. Expectations / use case (optional). The deprecated `bcdock auth waitlist`
	// command exposed --use-case; honour it as a fallback so existing scripts
	// don't silently drop the field when delegating to runJoinWaitlist.
	useCase, _ := cmd.Flags().GetString("expectations")
	if useCase == "" {
		fallback, _ := cmd.Flags().GetString("use-case")
		useCase = fallback
	}
	if useCase == "" && !cmd.Flags().Changed("expectations") && !cmd.Flags().Changed("use-case") {
		v, err := promptOptional("What would you like BCDock to help you with? (optional): ")
		if err != nil {
			return err
		}
		useCase = v
	}

	// 3. Name (required)
	name, _ := cmd.Flags().GetString("name")
	if name == "" {
		v, err := prompt("Name: ")
		if err != nil {
			return err
		}
		name = strings.TrimSpace(v)
	}
	if name == "" {
		return fmt.Errorf("name is required")
	}

	// 4. Email (required)
	email, _ := cmd.Flags().GetString("email")
	if email == "" {
		v, err := prompt("Email: ")
		if err != nil {
			return err
		}
		email = strings.TrimSpace(v)
	}
	if email == "" {
		return fmt.Errorf("email is required")
	}

	req := waitlistFullRequest{
		Name:            name,
		Email:           email,
		UseCase:         useCase,
		BcVersion:       bcVersion,
		Country:         country,
		ArtifactType:    artifactType,
		MultiTenant:     multiTenant,
		PreferredRegion: region,
		SourcePage:      "cli",
	}

	var resp struct {
		Message string `json:"message"`
	}
	if err := r.Client.Do(cmd.Context(), http.MethodPost, "/api/v1/public/waitlist", req, &resp); err != nil {
		return err
	}

	r.Printer.Info("%s", resp.Message)
	r.Printer.Info("Once an admin reviews your entry you'll receive an invite code by email.")
	r.Printer.Info("Then run: bcdock auth signup --invite-code CODE --email %s", email)
	return nil
}

// ── bcdock auth signup ────────────────────────────────────────────────────

var authSignupCmd = &cobra.Command{
	Use:   "signup",
	Short: "Activate your BCDock account using an invite code",
	Long: `Activate your account after receiving an invite code via email.

If you don't have an invite code yet, join the waitlist first:
  bcdock auth join-waitlist

Once you have a code, run:
  bcdock auth signup --invite-code ABCD1234 --email you@example.com

The CLI looks up your waitlist entry via the invite code and pre-fills any
BC config you supplied at waitlist time. After activation, run:
  bcdock auth login --email you@example.com

to receive an OTP and complete login. The first BC environment is queued
automatically when activation succeeds (if a complete config is available).

Exit codes:
  0   ok
  1   general error (invalid invite code, email mismatch)
  4   rate-limited`,
	Example: `  bcdock auth signup --invite-code ABCD1234 --email you@example.com
  bcdock auth signup --invite-code ABCD1234 --email you@example.com --bc-version 25.5 --country au`,
	RunE: runSignup,
}

func runSignup(cmd *cobra.Command, _ []string) error {
	r := GetResolved(cmd)

	inviteCode, _ := cmd.Flags().GetString("invite-code")
	email, _ := cmd.Flags().GetString("email")

	// No invite code - direct user to the waitlist. Done locally to avoid a
	// 400 round-trip; the API also returns this hint if hit directly.
	if inviteCode == "" {
		r.Printer.Info("BCDock is currently in early access. You'll need an invite code to sign up.")
		r.Printer.Info("If you don't have one yet, join the waitlist first:")
		r.Printer.Info("  bcdock auth join-waitlist")
		return fmt.Errorf("missing required flag: --invite-code (or run join-waitlist first)")
	}
	inviteCode = strings.TrimSpace(strings.ToUpper(inviteCode))

	if email == "" {
		v, err := prompt("Email: ")
		if err != nil {
			return err
		}
		email = strings.TrimSpace(v)
	}
	if email == "" {
		return fmt.Errorf("email is required")
	}

	// Pre-validate the code with the public endpoint to retrieve any BC config
	// the user already provided on the waitlist. A failure here uses the same
	// opaque "Email or invite code is not correct" message as the activation
	// step - we don't tell the user *why* it failed.
	var validation validateInviteCodeResponse
	if err := r.Client.Do(cmd.Context(), http.MethodPost, "/api/v1/public/validate-invite-code",
		map[string]string{"code": inviteCode}, &validation); err != nil {
		return err
	}
	if !validation.Valid {
		return fmt.Errorf("email or invite code is not correct")
	}

	// Apply pre-fill defaults from the waitlist entry; CLI flags still win.
	bcVersion, _ := cmd.Flags().GetString("bc-version")
	country, _ := cmd.Flags().GetString("country")
	artifactType, _ := cmd.Flags().GetString("artifact-type")
	region, _ := cmd.Flags().GetString("region")
	multiTenantFlag, _ := cmd.Flags().GetString("multi-tenant")
	name, _ := cmd.Flags().GetString("name")

	var multiTenant *bool
	if multiTenantFlag != "" {
		v := strings.EqualFold(multiTenantFlag, "true") || strings.EqualFold(multiTenantFlag, "yes")
		multiTenant = &v
	}

	if validation.WaitlistConfig != nil {
		cfg := validation.WaitlistConfig
		if bcVersion == "" {
			bcVersion = cfg.BcVersion
		}
		if country == "" {
			country = cfg.Country
		}
		if artifactType == "" {
			artifactType = cfg.ArtifactType
		}
		if region == "" {
			region = cfg.Region
		}
		if multiTenant == nil && cfg.MultiTenant != nil {
			multiTenant = cfg.MultiTenant
		}
		if name == "" {
			name = cfg.Name
		}
	}

	acceptEula, _ := cmd.Flags().GetBool("accept-eula")
	acceptInsiderEula, _ := cmd.Flags().GetBool("accept-insider-eula")

	req := signupRequest{
		Email:             email,
		InviteCode:        inviteCode,
		Name:              name,
		BcVersion:         bcVersion,
		Country:           country,
		ArtifactType:      artifactType,
		MultiTenant:       multiTenant,
		Region:            region,
		AcceptEula:        acceptEula,
		AcceptInsiderEula: acceptInsiderEula,
	}

	var resp signupResponse
	if err := r.Client.Do(cmd.Context(), http.MethodPost, "/api/v1/auth/signup", req, &resp); err != nil {
		return err
	}

	r.Printer.Info("Account activated. A welcome email has been sent to %s.", email)
	if resp.EnvironmentShortId != "" {
		r.Printer.Info("Your first BC environment (%s) is being provisioned.", resp.EnvironmentShortId)
	}
	r.Printer.Info("Next: bcdock auth login --email %s", email)

	return r.Printer.Print(resp)
}

// ── shared prompts ──────────────────────────────────────────────────────────

// promptOptional reads a line; an empty input is OK and returns "".
func promptOptional(label string) (string, error) {
	s, err := prompt(label)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(s), nil
}

// ── flag wiring + registration ────────────────────────────────────────────

func init() {
	// auth signup
	authSignupCmd.Flags().String("invite-code", "", "Invite code from your waitlist invite email")
	authSignupCmd.Flags().String("email", "", "Email address (the one your invite was issued for)")
	authSignupCmd.Flags().String("name", "", "Display name (defaults to waitlist entry name if any)")
	authSignupCmd.Flags().String("bc-version", "", "BC version (e.g. 27.5)")
	authSignupCmd.Flags().String("country", "", "BC country code (e.g. AU)")
	authSignupCmd.Flags().String("artifact-type", "", "Artifact type: Sandbox or OnPrem")
	authSignupCmd.Flags().String("multi-tenant", "", "true|false - multi-tenant container")
	authSignupCmd.Flags().String("region", "", "Azure region (e.g. australiaeast)")
	authSignupCmd.Flags().Bool("accept-eula", false, "Accept the Microsoft NAV/BC on Docker EULA")
	authSignupCmd.Flags().Bool("accept-insider-eula", false, "Accept the BC Insider EULA (preview versions)")

	// auth join-waitlist
	authJoinWaitlistCmd.Flags().String("name", "", "Your name (skips prompt)")
	authJoinWaitlistCmd.Flags().String("email", "", "Email address (skips prompt)")
	authJoinWaitlistCmd.Flags().String("expectations", "", "What you'd like BCDock to help you with (optional)")
	authJoinWaitlistCmd.Flags().String("bc-version", "", "Default BC version (optional)")
	authJoinWaitlistCmd.Flags().String("country", "", "Default BC country code (optional)")
	authJoinWaitlistCmd.Flags().String("artifact-type", "", "Default artifact type: Sandbox or OnPrem")
	authJoinWaitlistCmd.Flags().String("multi-tenant", "", "true|false - preferred multi-tenant default")
	authJoinWaitlistCmd.Flags().String("region", "", "Preferred Azure region (optional)")

	authCmd.AddCommand(authSignupCmd, authJoinWaitlistCmd)

	// Update the legacy "auth waitlist" command to delegate to join-waitlist's
	// handler, so existing scripts keep working. Mark it hidden so help text
	// doesn't surface two commands for the same job.
	authWaitlistCmd.Hidden = true
	authWaitlistCmd.Deprecated = "use `bcdock auth join-waitlist` instead"
	authWaitlistCmd.RunE = runJoinWaitlist
	// Re-register the same flags so the command keeps working with the new handler.
	authWaitlistCmd.Flags().String("expectations", "", "What you'd like BCDock to help you with (optional)")
	authWaitlistCmd.Flags().String("bc-version", "", "Default BC version (optional)")
	authWaitlistCmd.Flags().String("country", "", "Default BC country code (optional)")
	authWaitlistCmd.Flags().String("artifact-type", "", "Default artifact type")
	authWaitlistCmd.Flags().String("multi-tenant", "", "true|false")
	authWaitlistCmd.Flags().String("region", "", "Preferred Azure region")
}
