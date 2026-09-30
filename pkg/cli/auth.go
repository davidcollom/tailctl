package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"unicode"

	"github.com/davidcollom/tailctl/pkg/credentials"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

// Keep input bounded before passing it to platform keychain implementations.
const maxTokenBytes = 2048

func loginCommand(r *Runtime) *cobra.Command {
	var tokenStdin bool
	cmd := &cobra.Command{
		Use:   "login",
		Short: "Save an API token in the OS credential store",
		Long:  "Save an API access token or an already-issued OAuth access token. Interactive entry is hidden. Use --token-stdin for a pipe. No token is written to configuration files. This saves credentials; it does not start an OAuth browser flow or verify API permissions.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if _, err := credentials.Account(r.Config.Server); err != nil {
				return err
			}
			token, err := readToken(cmd, tokenStdin)
			if err != nil {
				return err
			}
			if err := cmd.Context().Err(); err != nil {
				return err
			}
			if err := r.Credentials.Set(r.Config.Server, token); err != nil {
				// Backend errors can contain provider details; never include secret input.
				return errors.New("could not save API token in the OS credential store; unlock or enable it and try again (no plaintext fallback)")
			}
			if _, err := fmt.Fprintln(cmd.OutOrStdout(), "API token saved in the OS credential store."); err != nil {
				return err
			}
			if r.Config.Token != "" {
				_, err = fmt.Fprintln(cmd.ErrOrStderr(), "A token from environment or configuration currently takes precedence over the saved token.")
			}
			return err
		},
	}
	cmd.Flags().BoolVar(&tokenStdin, "token-stdin", false, "Read a token from stdin instead of prompting")
	return cmd
}

func readToken(cmd *cobra.Command, tokenStdin bool) (string, error) {
	var data []byte
	var err error
	if tokenStdin {
		data, err = io.ReadAll(io.LimitReader(cmd.InOrStdin(), maxTokenBytes+1))
		if err != nil {
			return "", errors.New("could not read token from stdin")
		}
	} else {
		file, ok := cmd.InOrStdin().(*os.File)
		if !ok || !term.IsTerminal(int(file.Fd())) {
			return "", errors.New("interactive login requires a terminal; pipe a token using --token-stdin")
		}
		// Restore the previous terminal state on return, including Ctrl-C cancellation.
		state, err := term.GetState(int(file.Fd()))
		if err != nil {
			return "", errors.New("could not access terminal")
		}
		defer term.Restore(int(file.Fd()), state)
		if _, err := fmt.Fprint(cmd.ErrOrStderr(), "Tailscale API token (hidden): "); err != nil {
			return "", err
		}
		data, err = readPassword(cmd.Context(), int(file.Fd()))
		fmt.Fprintln(cmd.ErrOrStderr())
		if err != nil {
			return "", errors.New("could not read token from terminal")
		}
	}
	// Erase the mutable input buffer after converting it. Go strings cannot be zeroed.
	defer clear(data)
	if len(data) > maxTokenBytes {
		return "", fmt.Errorf("API token exceeds %d bytes", maxTokenBytes)
	}
	token := strings.TrimSpace(string(data))
	if token == "" {
		return "", errors.New("API token must not be empty")
	}
	for _, ch := range token {
		if unicode.IsSpace(ch) || unicode.IsControl(ch) {
			return "", errors.New("API token must be a single value without whitespace or control characters")
		}
	}
	if strings.HasPrefix(token, "tskey-auth-") {
		return "", errors.New("device enrolment auth keys cannot access the API; use an API access token or OAuth access token")
	}
	return token, nil
}

// readPassword allows the command to return promptly on Ctrl-C. The main CLI
// exits after cancellation; a pending terminal read is terminated with it.
func readPassword(ctx context.Context, fd int) ([]byte, error) {
	type result struct {
		data []byte
		err  error
	}
	results := make(chan result)
	go func() {
		data, err := term.ReadPassword(fd)
		select {
		case results <- result{data: data, err: err}:
		case <-ctx.Done():
			clear(data)
		}
	}()
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case r := <-results:
		return r.data, r.err
	}
}

func logoutCommand(r *Runtime) *cobra.Command {
	return &cobra.Command{
		Use:   "logout",
		Short: "Remove the saved API token from the OS credential store",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if _, err := credentials.Account(r.Config.Server); err != nil {
				return err
			}
			if err := cmd.Context().Err(); err != nil {
				return err
			}
			err := r.Credentials.Delete(r.Config.Server)
			if err != nil && !errors.Is(err, credentials.ErrNotFound) {
				return errors.New("could not remove API token from the OS credential store; unlock or enable it and try again")
			}
			message := "Saved API token removed from the OS credential store."
			if errors.Is(err, credentials.ErrNotFound) {
				message = "No saved API token in the OS credential store."
			}
			if _, err := fmt.Fprintln(cmd.OutOrStdout(), message); err != nil {
				return err
			}
			if r.Config.Token != "" {
				_, err = fmt.Fprintln(cmd.ErrOrStderr(), "A token from environment or configuration is still active; remove that override to stop using it.")
			}
			return err
		},
	}
}
