package isolated

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"syscall"
)

// DataBinding is derived only by the activation controller from accepted roots
// and the directory-exchange journal, never decoded from an RPC request.
type DataBinding struct{ Host, Guest string }

// ManagedArguments compiles the separate, formally activated runtime policy.
// It intentionally shares the host network. Run/GatewayProbe remain completely
// isolated and have no switch that can select this policy. systemd executes
// bwrap directly; no porter daemon or listener participates in daily running.
func ManagedArguments(w *Workspace, s Spec, data []DataBinding) ([]string, error) {
	return managedArguments(w, s, data, true)
}

// PrepareNetwork is only for the trusted management preparation controller,
// before this workspace is exposed for daily execution. It copies public OS
// resolver/certificates, never Agent data or credentials.
func PrepareNetwork(w *Workspace) error {
	if w == nil || runtime.GOOS != "linux" {
		return ErrUnsupported
	}
	if err := managedDNS(w); err != nil {
		return err
	}
	return managedCertificates(w)
}

// AttachedArguments uses already prepared public network files. Daily native
// commands must not initialize or rewrite a running workspace's DNS/CA files.
func AttachedArguments(w *Workspace, s Spec, data []DataBinding) ([]string, error) {
	return managedArguments(w, s, data, false)
}

func managedArguments(w *Workspace, s Spec, data []DataBinding, prepare bool) ([]string, error) {
	if w == nil || !privatePathAt(s.home(), s.firstArgv()) {
		return nil, ErrWorkspace
	}
	entry := strings.TrimPrefix(s.firstArgv(), s.home()+"/")
	privateEntry := false
	for _, program := range w.programs {
		if strings.HasPrefix(entry, program+"/") {
			privateEntry = true
		}
	}
	if !privateEntry {
		return nil, ErrWorkspace
	}
	if runtime.GOOS == "linux" && prepare {
		if err := managedDNS(w); err != nil {
			return nil, err
		}
		if err := managedCertificates(w); err != nil {
			return nil, err
		}
	}
	if runtime.GOOS == "linux" && !prepare {
		for _, rel := range []string{"resolv.conf", "ssl/certs/ca-certificates.crt"} {
			st, err := os.Lstat(filepath.Join(w.dir, "etc", rel))
			if err != nil || !st.Mode().IsRegular() || st.Mode().Perm()&0022 != 0 {
				return nil, ErrWorkspace
			}
		}
	}
	args, err := arguments(w, s)
	if err != nil || len(data) == 0 {
		return nil, ErrWorkspace
	}
	workspace, err := filepath.EvalSymlinks(w.dir)
	if err != nil {
		return nil, ErrWorkspace
	}
	loginHome, _ := os.UserHomeDir()
	if resolved, err := filepath.EvalSymlinks(loginHome); err == nil {
		loginHome = resolved
	}
	for i, b := range data {
		if !filepath.IsAbs(b.Host) || filepath.Clean(b.Host) != b.Host || b.Host == s.home() || !privatePathAt(s.home(), b.Guest) || b.Guest == s.home() || strings.ContainsAny(b.Host, "\x00\r\n") {
			return nil, ErrWorkspace
		}
		resolved, err := filepath.EvalSymlinks(b.Host)
		st, statErr := os.Lstat(b.Host)
		if err != nil || statErr != nil || resolved != b.Host || overlap(resolved, workspace) || resolved == loginHome || !st.IsDir() || st.Mode().Perm()&0022 != 0 {
			return nil, ErrWorkspace
		}
		owner, ok := st.Sys().(*syscall.Stat_t)
		if !ok || owner.Uid != uint32(os.Getuid()) {
			return nil, ErrWorkspace
		}
		for _, other := range data[:i] {
			if overlap(b.Guest, other.Guest) || overlap(b.Host, other.Host) {
				return nil, ErrWorkspace
			}
		}
		for _, program := range w.programs {
			if b.Guest == s.home()+"/"+program || strings.HasPrefix(b.Guest, s.home()+"/"+program+"/") {
				return nil, ErrWorkspace
			}
		}
	}
	// The only shared namespace is networking, explicitly enabled for formal
	// activation. Private PID/HOME/proc/run and read-only programs stay intact.
	if len(args) == 0 || args[0] != "--unshare-all" {
		return nil, ErrWorkspace
	}
	args = append(args[:1], append([]string{"--share-net"}, args[1:]...)...)
	atCert := -1
	for i := 0; i < len(args); i++ {
		if args[i] == "--chdir" {
			atCert = i
			break
		}
	}
	if atCert < 0 {
		return nil, ErrWorkspace
	}
	certEnv := []string{"--setenv", "SSL_CERT_FILE", "/etc/ssl/certs/ca-certificates.crt", "--setenv", "SSL_CERT_DIR", "/etc/ssl/certs"}
	args = append(append(append([]string{}, args[:atCert]...), certEnv...), args[atCert:]...)
	at := slices.Index(args, "--proc")
	if at < 0 {
		return nil, ErrWorkspace
	}
	// Data mounts must precede the program overlays, so state roots cannot
	// replace the read-only program files. Insert just after the HOME bind.
	at = -1
	for i := 0; i+2 < len(args); i++ {
		if args[i] == "--bind" && args[i+1] == w.HomeDir() && args[i+2] == s.home() {
			at = i + 3
			break
		}
	}
	if at < 0 {
		return nil, ErrWorkspace
	}
	var mounts []string
	for _, b := range data {
		mounts = append(mounts, "--bind", b.Host, b.Guest)
	}
	return append(append(append([]string{}, args[:at]...), mounts...), args[at:]...), nil
}

func (s Spec) firstArgv() string {
	if len(s.Argv) == 0 {
		return ""
	}
	return s.Argv[0]
}

// Only the public OS resolver configuration is copied; host /etc stays hidden.
func managedDNS(w *Workspace) error {
	f, err := os.Open("/etc/resolv.conf")
	if err != nil {
		return ErrWorkspace
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil || !st.Mode().IsRegular() || st.Size() > 64<<10 || st.Mode().Perm()&0022 != 0 {
		return ErrWorkspace
	}
	owner, ok := st.Sys().(*syscall.Stat_t)
	// A root-controlled /etc may delegate this public file to the resolver
	// service account. Never accept a caller-owned replacement as OS facts.
	parent, parentErr := os.Stat("/etc")
	var parentOwner *syscall.Stat_t
	if parentErr == nil {
		parentOwner, _ = parent.Sys().(*syscall.Stat_t)
	}
	if !ok || parentErr != nil || parentOwner == nil || parentOwner.Uid != 0 || parent.Mode().Perm()&0022 != 0 || owner.Uid != 0 && owner.Uid == uint32(os.Getuid()) {
		return ErrWorkspace
	}
	b, err := io.ReadAll(io.LimitReader(f, 64<<10+1))
	if err != nil || len(b) > 64<<10 {
		return ErrWorkspace
	}
	target := filepath.Join(w.dir, "etc", "resolv.conf")
	out, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if os.IsExist(err) {
		existing, err := os.ReadFile(target)
		if err != nil || !bytes.Equal(existing, b) {
			return ErrWorkspace
		}
		return nil
	}
	if err != nil {
		return ErrWorkspace
	}
	_, err = out.Write(b)
	if err == nil {
		err = out.Sync()
	}
	closed := out.Close()
	if err != nil || closed != nil {
		return ErrWorkspace
	}
	return nil
}
