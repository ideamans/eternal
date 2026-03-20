package main

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/signal"
	"runtime"
	"strings"
	"syscall"
	"text/tabwriter"
	"time"

	"github.com/ideamans/eternal/pkg/agent"
	"github.com/ideamans/eternal/pkg/client"
	"github.com/ideamans/eternal/pkg/protocol"
	"github.com/ideamans/eternal/pkg/server"
	"github.com/ideamans/eternal/pkg/watcher"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

//go:embed all:dist
var webDist embed.FS

var defaultAddr = "http://127.0.0.1:2840"

func main() {
	rootCmd := &cobra.Command{
		Use:     "et",
		Short:   "Eternal - Persistent Terminal Session Manager",
		Version: version,
	}

	rootCmd.PersistentFlags().StringP("server", "s", defaultAddr, "Server address")

	rootCmd.AddCommand(
		serverCmd(),
		runCmd(),
		attachCmd(),
		lsCmd(),
		killCmd(),
		installCmd(),
		uninstallCmd(),
		upgradeCmd(),
		agentCmd(),
	)

	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

func getClient(cmd *cobra.Command) *client.Client {
	addr, _ := cmd.Flags().GetString("server")
	return client.New(addr)
}

func serverCmd() *cobra.Command {
	var port int
	var host string
	var peers []string

	cmd := &cobra.Command{
		Use:   "server",
		Short: "Start the eternal server daemon",
		RunE: func(cmd *cobra.Command, args []string) error {
			server.WebDist = webDist
			server.Version = version

			// Set ExecPath for spawning agents
			execPath, err := os.Executable()
			if err != nil {
				return fmt.Errorf("cannot determine executable path: %w", err)
			}
			server.ExecPath = execPath

			// Merge --peer flags and ET_PEERS env var
			allPeers := append([]string{}, peers...)
			if envPeers := os.Getenv("ET_PEERS"); envPeers != "" {
				for _, p := range strings.Split(envPeers, ",") {
					p = strings.TrimSpace(p)
					if p != "" {
						allPeers = append(allPeers, p)
					}
				}
			}
			for i, p := range allPeers {
				allPeers[i] = normalizePeerAddr(p)
			}
			server.Peers = allPeers

			s := server.New()

			// Watch binary for changes and auto-restart
			go watcher.WatchBinary(execPath, func() {
				log.Println("Binary changed, shutting down for restart...")
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				s.Shutdown(ctx)
			})

			addr := fmt.Sprintf("%s:%d", host, port)
			err = s.ListenAndServe(addr)
			if err != nil && err.Error() == "http: Server closed" {
				// Graceful shutdown for auto-restart
				log.Println("Server shut down, re-executing...")
				return syscall.Exec(execPath, os.Args, os.Environ())
			}
			return err
		},
	}
	cmd.Flags().IntVar(&port, "port", 2840, "Port to listen on")
	cmd.Flags().StringVar(&host, "host", "0.0.0.0", "Host to bind to")
	cmd.Flags().StringArrayVar(&peers, "peer", nil, "Peer server address for session aggregation (can be specified multiple times)")
	return cmd
}

// normalizePeerAddr ensures the peer address has a scheme and port.
func normalizePeerAddr(addr string) string {
	addr = strings.TrimRight(addr, "/")
	if !strings.HasPrefix(addr, "http://") && !strings.HasPrefix(addr, "https://") {
		addr = "http://" + addr
	}
	// Add default port if not specified
	// Strip scheme, check for port, re-add scheme
	withoutScheme := addr
	scheme := "http://"
	if strings.HasPrefix(addr, "https://") {
		scheme = "https://"
		withoutScheme = strings.TrimPrefix(addr, "https://")
	} else {
		withoutScheme = strings.TrimPrefix(addr, "http://")
	}
	if !strings.Contains(withoutScheme, ":") {
		withoutScheme = withoutScheme + ":2840"
	}
	return scheme + withoutScheme
}

func runCmd() *cobra.Command {
	var name string

	cmd := &cobra.Command{
		Use:   "run [command] [args...]",
		Short: "Run a command in a new persistent session (default: current shell)",
		RunE: func(cmd *cobra.Command, args []string) error {
			c := getClient(cmd)

			if len(args) == 0 {
				shell := os.Getenv("SHELL")
				if shell == "" {
					shell = "/bin/sh"
				}
				args = []string{shell}
			}

			cols, rows, err := term.GetSize(int(os.Stdin.Fd()))
			if err != nil {
				cols, rows = 80, 24
			}

			cwd, _ := os.Getwd()

			sess, err := c.CreateSession(client.CreateRequest{
				Name:    name,
				Command: args,
				Dir:     cwd,
				Cols:    cols,
				Rows:    rows,
			})
			if err != nil {
				return err
			}

			fmt.Fprintf(os.Stderr, "Session %s started (%s)\n", sess.ID, strings.Join(args, " "))
			return attachToSession(c, sess.ID)
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "Session name")
	return cmd
}

func attachCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "attach [name|id]",
		Short: "Attach to an existing session",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c := getClient(cmd)

			// Resolve name/id via API
			sessions, err := c.ListSessions()
			if err != nil {
				return err
			}

			var sessionID string
			for _, s := range sessions {
				if s.ID == args[0] || s.Name == args[0] {
					sessionID = s.ID
					break
				}
				if len(args[0]) >= 4 && strings.HasPrefix(s.ID, args[0]) {
					sessionID = s.ID
					break
				}
			}
			if sessionID == "" {
				return fmt.Errorf("session not found: %s", args[0])
			}

			return attachToSession(c, sessionID)
		},
	}
}

func lsCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "ls",
		Short: "List active sessions",
		RunE: func(cmd *cobra.Command, args []string) error {
			c := getClient(cmd)
			sessions, err := c.ListSessions()
			if err != nil {
				return err
			}

			if len(sessions) == 0 {
				fmt.Println("No active sessions.")
				return nil
			}

			w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
			fmt.Fprintln(w, "ID\tNAME\tDIR\tCOMMAND\tCLIENTS\tCREATED\tLAST USED")
			for _, s := range sessions {
				dir := s.Dir
				if dir == "" {
					dir = "-"
				}
				fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%d\t%s\t%s\n",
					s.ID[:8],
					s.Name,
					dir,
					strings.Join(s.Command, " "),
					s.Clients,
					formatTime(s.CreatedAt),
					formatTime(s.LastUsed),
				)
			}
			return w.Flush()
		},
	}
}

func killCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "kill [name|id]",
		Short: "Kill a session",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c := getClient(cmd)

			// Resolve name/id
			sessions, err := c.ListSessions()
			if err != nil {
				return err
			}

			var sessionID string
			for _, s := range sessions {
				if s.ID == args[0] || s.Name == args[0] {
					sessionID = s.ID
					break
				}
				if len(args[0]) >= 4 && strings.HasPrefix(s.ID, args[0]) {
					sessionID = s.ID
					break
				}
			}
			if sessionID == "" {
				return fmt.Errorf("session not found: %s", args[0])
			}

			if err := c.KillSession(sessionID); err != nil {
				return err
			}
			fmt.Fprintf(os.Stderr, "Session %s killed.\n", sessionID[:8])
			return nil
		},
	}
}

func attachToSession(c *client.Client, sessionID string) error {
	conn, err := c.ConnectWebSocket(sessionID)
	if err != nil {
		return err
	}
	defer conn.Close()

	// Set terminal to raw mode
	oldState, err := term.MakeRaw(int(os.Stdin.Fd()))
	if err != nil {
		return fmt.Errorf("failed to set raw mode: %w", err)
	}
	defer term.Restore(int(os.Stdin.Fd()), oldState)

	// Send initial size
	cols, rows, _ := term.GetSize(int(os.Stdin.Fd()))
	client.SendResize(conn, cols, rows)

	// Handle SIGWINCH for terminal resize
	sigwinch := make(chan os.Signal, 1)
	signal.Notify(sigwinch, syscall.SIGWINCH)
	go func() {
		for range sigwinch {
			cols, rows, _ := term.GetSize(int(os.Stdin.Fd()))
			client.SendResize(conn, cols, rows)
		}
	}()

	done := make(chan struct{})

	// Read from WebSocket → stdout
	go func() {
		defer close(done)
		for {
			_, raw, err := conn.ReadMessage()
			if err != nil {
				return
			}
			var msg protocol.Message
			if err := json.Unmarshal(raw, &msg); err != nil {
				continue
			}
			switch msg.Type {
			case protocol.TypeOutput:
				os.Stdout.Write(msg.Data)
			case protocol.TypeExit:
				return
			}
		}
	}()

	// Read from stdin → WebSocket
	go func() {
		buf := make([]byte, 4096)
		for {
			n, err := os.Stdin.Read(buf)
			if err != nil {
				conn.Close()
				return
			}
			if err := client.SendInput(conn, buf[:n]); err != nil {
				return
			}
		}
	}()

	<-done
	return nil
}

func installCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "install",
		Short: "Output daemon install script for this platform",
		Run: func(cmd *cobra.Command, args []string) {
			script := generateInstallScript()
			fmt.Print(script)
		},
	}
}

func generateInstallScript() string {
	switch runtime.GOOS {
	case "darwin":
		return macOSInstallScript()
	case "linux":
		return linuxInstallScript()
	default:
		return fmt.Sprintf("#!/bin/bash\necho 'Unsupported platform: %s'\necho 'Please run \"et server\" manually or set up a service yourself.'\nexit 1\n", runtime.GOOS)
	}
}

func macOSInstallScript() string {
	return `#!/bin/bash
set -eu

# -----------------------------------------------------------
# Eternal Terminal - macOS LaunchAgent Installer
# -----------------------------------------------------------
#
# This script installs "et server" as a launchd user agent.
# The server starts automatically on login and restarts on crash.
#
# Management commands:
#   Status:    launchctl list | grep com.eternal.et
#   Logs:      tail -f /tmp/eternal.log
#   Restart:   launchctl kickstart -k gui/$(id -u)/com.eternal.et
#   Stop:      launchctl bootout gui/$(id -u)/com.eternal.et
#   Disable:   launchctl bootout gui/$(id -u)/com.eternal.et && rm ~/Library/LaunchAgents/com.eternal.et.plist
#   Uninstall: launchctl bootout gui/$(id -u)/com.eternal.et 2>/dev/null; rm -f ~/Library/LaunchAgents/com.eternal.et.plist
# -----------------------------------------------------------

ET_BIN="$(which et)"
PLIST="$HOME/Library/LaunchAgents/com.eternal.et.plist"
DOMAIN="gui/$(id -u)"

mkdir -p "$HOME/Library/LaunchAgents"

cat > "$PLIST" << PLIST_EOF
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN"
  "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>Label</key>
    <string>com.eternal.et</string>
    <key>ProgramArguments</key>
    <array>
        <string>${ET_BIN}</string>
        <string>server</string>
    </array>
    <key>RunAtLoad</key>
    <true/>
    <key>KeepAlive</key>
    <true/>
    <key>StandardOutPath</key>
    <string>/tmp/eternal.log</string>
    <key>StandardErrorPath</key>
    <string>/tmp/eternal.err</string>
</dict>
</plist>
PLIST_EOF

launchctl bootout "$DOMAIN/com.eternal.et" 2>/dev/null || true
launchctl bootstrap "$DOMAIN" "$PLIST"

echo "eternal server installed and started."
echo "  Auto-start: enabled (RunAtLoad)"
echo "  Status:     launchctl list | grep com.eternal.et"
echo "  Logs:       tail -f /tmp/eternal.log"
echo "  Restart:    launchctl kickstart -k $DOMAIN/com.eternal.et"
echo "  Uninstall:  launchctl bootout $DOMAIN/com.eternal.et && rm $PLIST"
`
}

func linuxInstallScript() string {
	return `#!/bin/bash
set -eu

# -----------------------------------------------------------
# Eternal Terminal - systemd User Service Installer
# -----------------------------------------------------------
#
# This script installs "et server" as a systemd user service.
# The server starts automatically on boot (via linger) and
# restarts on crash.
#
# Management commands:
#   Status:    systemctl --user status eternal.service
#   Logs:      journalctl --user -u eternal.service -f
#   Restart:   systemctl --user restart eternal.service
#   Stop:      systemctl --user stop eternal.service
#   Disable:   systemctl --user disable eternal.service
#   Uninstall: systemctl --user stop eternal.service && systemctl --user disable eternal.service && rm ~/.config/systemd/user/eternal.service && systemctl --user daemon-reload
# -----------------------------------------------------------

ET_BIN="$(which et)"
UNIT_DIR="$HOME/.config/systemd/user"
mkdir -p "$UNIT_DIR"

cat > "$UNIT_DIR/eternal.service" << UNIT_EOF
[Unit]
Description=Eternal Terminal Session Manager
After=network.target

[Service]
Type=simple
ExecStart=${ET_BIN} server
Restart=always
RestartSec=3

[Install]
WantedBy=default.target
UNIT_EOF

systemctl --user daemon-reload
systemctl --user enable eternal.service
systemctl --user start eternal.service

loginctl enable-linger "$(whoami)" 2>/dev/null || true

echo "eternal server installed and started."
echo "  Auto-start: enabled (systemd enable + linger)"
echo "  Status:     systemctl --user status eternal.service"
echo "  Logs:       journalctl --user -u eternal.service -f"
echo "  Restart:    systemctl --user restart eternal.service"
echo "  Uninstall:  systemctl --user stop eternal.service && systemctl --user disable eternal.service && rm $UNIT_DIR/eternal.service"
`
}

func uninstallCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "uninstall",
		Short: "Output daemon uninstall script for this platform",
		Run: func(cmd *cobra.Command, args []string) {
			script := generateUninstallScript()
			fmt.Print(script)
		},
	}
}

func generateUninstallScript() string {
	switch runtime.GOOS {
	case "darwin":
		return macOSUninstallScript()
	case "linux":
		return linuxUninstallScript()
	default:
		return fmt.Sprintf("#!/bin/bash\necho 'Unsupported platform: %s'\nexit 1\n", runtime.GOOS)
	}
}

func macOSUninstallScript() string {
	return `#!/bin/bash
set -eu

# -----------------------------------------------------------
# Eternal Terminal - macOS LaunchAgent Uninstaller
# -----------------------------------------------------------

PLIST="$HOME/Library/LaunchAgents/com.eternal.et.plist"
DOMAIN="gui/$(id -u)"

launchctl bootout "$DOMAIN/com.eternal.et" 2>/dev/null || true
rm -f "$PLIST"

echo "eternal server uninstalled."
echo "  The plist has been removed and the service has been stopped."
`
}

func linuxUninstallScript() string {
	return `#!/bin/bash
set -eu

# -----------------------------------------------------------
# Eternal Terminal - systemd User Service Uninstaller
# -----------------------------------------------------------

UNIT_DIR="$HOME/.config/systemd/user"

systemctl --user stop eternal.service 2>/dev/null || true
systemctl --user disable eternal.service 2>/dev/null || true
rm -f "$UNIT_DIR/eternal.service"
systemctl --user daemon-reload

echo "eternal server uninstalled."
echo "  The service has been stopped, disabled, and removed."
`
}

func agentCmd() *cobra.Command {
	var id, name, dir string
	var cols, rows int

	cmd := &cobra.Command{
		Use:    "agent",
		Short:  "Run a session agent (internal)",
		Hidden: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				shell := os.Getenv("SHELL")
				if shell == "" {
					shell = "/bin/sh"
				}
				args = []string{shell}
			}
			return agent.Run(agent.Options{
				ID:      id,
				Name:    name,
				Command: args,
				Dir:     dir,
				Cols:    cols,
				Rows:    rows,
			})
		},
	}
	cmd.Flags().StringVar(&id, "id", "", "Session ID")
	cmd.Flags().StringVar(&name, "name", "", "Session name")
	cmd.Flags().StringVar(&dir, "dir", "", "Working directory")
	cmd.Flags().IntVar(&cols, "cols", 80, "Terminal columns")
	cmd.Flags().IntVar(&rows, "rows", 24, "Terminal rows")
	return cmd
}

func formatTime(t time.Time) string {
	if t.IsZero() {
		return "-"
	}
	return t.Format("15:04:05")
}
