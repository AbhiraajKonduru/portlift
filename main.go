// Command portlift puts a local server on a public URL through a Cloudflare
// quick tunnel.
package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"

	"github.com/mdp/qrterminal/v3"
	"golang.org/x/term"

	"github.com/AbhiraajKonduru/portlift/internal/cloudflared"
	"github.com/AbhiraajKonduru/portlift/internal/detect"
)

var version = "dev"

const usage = `portlift puts a local server on a public URL.

Usage:
  portlift            expose the server started from this folder
  portlift <port>     expose a specific port
  portlift <url>      expose a specific local URL

Flags:
  --qr            show a QR code for the URL on startup
  -q, --quiet     print only the URL
  -v, --version   print the version
  -h, --help      show this help

Keys while the tunnel is up:
  c               copy the URL to the clipboard
  r               show a QR code for the URL
  Ctrl+C          stop
`

// errQuit means the user backed out; there is nothing more to report.
var errQuit = errors.New("cancelled")

type options struct {
	target  string
	qr      bool
	quiet   bool
	help    bool
	version bool
}

func parseArgs(args []string) (options, error) {
	var opts options
	for _, arg := range args {
		switch arg {
		case "--qr":
			opts.qr = true
		case "-q", "--quiet":
			opts.quiet = true
		case "-h", "--help":
			opts.help = true
		case "-v", "--version":
			opts.version = true
		default:
			if strings.HasPrefix(arg, "-") {
				return opts, fmt.Errorf("unknown flag %s", arg)
			}
			if opts.target != "" {
				return opts, errors.New("expected at most one port or URL")
			}
			opts.target = arg
		}
	}
	return opts, nil
}

func main() {
	os.Exit(run(os.Args[1:]))
}

func run(args []string) int {
	opts, err := parseArgs(args)
	if err != nil {
		fmt.Fprintf(os.Stderr, "portlift: %v\n\n%s", err, usage)
		return 2
	}
	if opts.help {
		fmt.Print(usage)
		return 0
	}
	if opts.version {
		fmt.Println("portlift", version)
		return 0
	}

	// Anything beyond the bare URL, and any reading of the keyboard, is only
	// for a person at a terminal.
	interactive := !opts.quiet && term.IsTerminal(int(os.Stdin.Fd())) && term.IsTerminal(int(os.Stdout.Fd()))

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	if err := serve(ctx, opts, interactive); err != nil && !errors.Is(err, errQuit) && ctx.Err() == nil {
		fmt.Fprintf(os.Stderr, "portlift: %v\n", err)
		return 1
	}
	return 0
}

func serve(ctx context.Context, opts options, interactive bool) error {
	origin, label, err := resolve(ctx, opts.target, interactive)
	if err != nil {
		return err
	}
	bin, err := ensureCloudflared(ctx, interactive)
	if err != nil {
		return err
	}

	if interactive {
		if label != "" {
			fmt.Fprintln(os.Stderr, label)
		}
		if cfg := cloudflared.UserConfig(); cfg != "" {
			fmt.Fprintf(os.Stderr, "warning: %s exists; Cloudflare does not support quick tunnels while it does\n", cfg)
		}
		fmt.Fprint(os.Stderr, "connecting…")
	}
	blocked := make(chan *detect.Block, 1)
	if interactive {
		go func() { blocked <- detect.HostBlock(ctx, origin) }()
	}

	tunnel, err := cloudflared.Start(ctx, bin, origin, strings.HasPrefix(origin, "https://"))
	if interactive {
		fmt.Fprint(os.Stderr, "\r            \r")
	}
	if err != nil {
		return err
	}
	defer tunnel.Stop()

	if !interactive {
		fmt.Println(tunnel.URL)
		select {
		case <-ctx.Done():
			return nil
		case <-tunnel.Done():
			return tunnel.Err()
		}
	}

	if b := <-blocked; b != nil {
		fmt.Fprintf(os.Stderr, "warning: %s is rejecting requests that come through the tunnel.\n  To fix it, %s\n", b.Server, b.Fix)
	}

	// Raw mode delivers single key presses, at the cost of Ctrl+C arriving as
	// a byte rather than a signal and of newlines needing a carriage return.
	out := io.Writer(os.Stdout)
	hint := "Ctrl+C: stop"
	keys := make(chan byte)
	stdin := int(os.Stdin.Fd())
	if state, err := term.MakeRaw(stdin); err == nil {
		defer term.Restore(stdin, state)
		out = crlfWriter{os.Stdout}
		if !opts.qr {
			hint = "r: QR code · " + hint
		}
		hint = "c: copy URL · " + hint
		go func() {
			buf := make([]byte, 1)
			for {
				n, err := os.Stdin.Read(buf)
				if err != nil {
					return
				}
				if n == 1 {
					keys <- buf[0]
				}
			}
		}()
	}

	fmt.Fprintf(out, "%s\n%s\n", tunnel.URL, hint)

	// A note is a one-line message that the next note overwrites in place,
	// so pressing a key repeatedly does not scroll the URL away.
	noteLen := -1 // length of the note the cursor is sitting after, or -1
	note := func(msg string) {
		fmt.Fprintf(out, "\r%s%s", msg, strings.Repeat(" ", max(0, noteLen-len(msg))))
		noteLen = len(msg)
	}
	endNote := func() {
		if noteLen >= 0 {
			fmt.Fprintln(out)
			noteLen = -1
		}
	}
	defer endNote()

	qrShown := false
	showQR := func() {
		if qrShown {
			return
		}
		qrShown = true
		endNote()
		fmt.Fprintln(out)
		qrterminal.GenerateHalfBlock(tunnel.URL, qrterminal.L, out)
	}
	if opts.qr {
		showQR()
	}

	const ctrlC = 3
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-tunnel.Done():
			return tunnel.Err()
		case key := <-keys:
			switch key {
			case 'r', 'R':
				showQR()
			case 'c', 'C':
				if err := copyToClipboard(tunnel.URL); err != nil {
					note("couldn't copy the URL: " + err.Error())
				} else {
					note("copied to clipboard")
				}
			case ctrlC:
				return nil
			}
		}
	}
}

// resolve works out which local URL to expose: the one named on the command
// line, or the server found running from this folder. label describes a
// detected server so the user can see what is about to go public.
func resolve(ctx context.Context, target string, interactive bool) (origin, label string, err error) {
	if target != "" {
		origin, err = parseTarget(ctx, target)
		return origin, "", err
	}

	res, err := detect.Find(ctx)
	if err != nil {
		return "", "", err
	}
	if len(res.Servers) == 0 {
		return "", "", notFound(res)
	}
	if s := res.Auto(); s != nil {
		return s.URL, s.String(), nil
	}

	if !interactive {
		var list strings.Builder
		for _, s := range res.Servers {
			fmt.Fprintf(&list, "\n  %s", s)
		}
		return "", "", fmt.Errorf("can't choose a server without asking; run `portlift <port>` with one of these:%s", list.String())
	}
	if len(res.Servers) == 1 {
		s := res.Servers[0]
		fmt.Fprintf(os.Stderr, "%s\nThis server was started from another part of the repo, not from this folder.\n", s)
		answer, err := ask(ctx, "Expose it? [y/N] ")
		if err != nil {
			return "", "", err
		}
		if answer = strings.ToLower(answer); answer != "y" && answer != "yes" {
			return "", "", errQuit
		}
		return s.URL, "", nil
	}
	fmt.Fprintln(os.Stderr, "More than one server is running here:")
	for i, s := range res.Servers {
		fmt.Fprintf(os.Stderr, "  %d) %s\n", i+1, s)
	}
	answer, err := ask(ctx, fmt.Sprintf("Which one? [1-%d] ", len(res.Servers)))
	if err != nil {
		return "", "", err
	}
	n, err := strconv.Atoi(answer)
	if err != nil || n < 1 || n > len(res.Servers) {
		return "", "", errQuit
	}
	return res.Servers[n-1].URL, "", nil
}

func parseTarget(ctx context.Context, target string) (string, error) {
	if port, err := strconv.Atoi(target); err == nil {
		if port < 1 || port > 65535 {
			return "", fmt.Errorf("%d is not a valid port", port)
		}
		origin, ok := detect.Probe(ctx, port)
		if !ok {
			return "", fmt.Errorf("nothing answered on localhost:%d; is the server running?", port)
		}
		return origin, nil
	}
	u, err := url.Parse(target)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return "", fmt.Errorf("%q is not a port or an http(s) URL", target)
	}
	return target, nil
}

func notFound(res detect.Result) error {
	msg := "no server found running from " + res.Cwd
	if res.Root == "" {
		msg += "\n  this folder is not in a git repo, so only it and the folders below it were checked"
	} else if res.Root != res.Cwd {
		msg += "\n  also checked the rest of the repo at " + res.Root
	}
	return errors.New(msg + "\n  to expose a specific port, run `portlift <port>`")
}

// ensureCloudflared returns a cloudflared to run, offering to download one if
// none is installed.
func ensureCloudflared(ctx context.Context, interactive bool) (string, error) {
	if bin, ok := cloudflared.Locate(); ok {
		return bin, nil
	}
	size, err := cloudflared.DownloadSize()
	if err != nil || !interactive {
		return "", fmt.Errorf("cloudflared is not installed; get it from %s", cloudflared.InstallDocs)
	}
	dest, err := cloudflared.CachePath()
	if err != nil {
		return "", err
	}
	fmt.Fprintf(os.Stderr, "portlift needs cloudflared, which is not installed.\nDownload version %s (%d MB) from github.com/cloudflare/cloudflared to\n  %s\n",
		cloudflared.Version, size>>20, dest)
	answer, err := ask(ctx, "Download it? [Y/n] ")
	if err != nil {
		return "", err
	}
	if answer = strings.ToLower(answer); answer != "" && answer != "y" && answer != "yes" {
		return "", errQuit
	}
	last := int64(-1)
	bin, err := cloudflared.Download(ctx, func(done, total int64) {
		if pct := done * 100 / total; pct != last {
			last = pct
			fmt.Fprintf(os.Stderr, "\rdownloading… %3d%%", pct)
		}
	})
	fmt.Fprint(os.Stderr, "\r                  \r")
	return bin, err
}

// ask prints a prompt and reads one line from the terminal, giving up if the
// user presses Ctrl+C.
func ask(ctx context.Context, prompt string) (string, error) {
	fmt.Fprint(os.Stderr, prompt)
	type reply struct {
		line string
		err  error
	}
	got := make(chan reply, 1)
	go func() {
		line, err := bufio.NewReader(os.Stdin).ReadString('\n')
		got <- reply{line, err}
	}()
	select {
	case r := <-got:
		if r.err != nil && r.line == "" {
			return "", errQuit
		}
		return strings.TrimSpace(r.line), nil
	case <-ctx.Done():
		fmt.Fprintln(os.Stderr)
		return "", errQuit
	}
}

// crlfWriter turns "\n" into "\r\n" for a terminal in raw mode.
type crlfWriter struct{ w io.Writer }

func (c crlfWriter) Write(p []byte) (int, error) {
	if _, err := c.w.Write([]byte(strings.ReplaceAll(string(p), "\n", "\r\n"))); err != nil {
		return 0, err
	}
	return len(p), nil
}
