package session

import (
	"errors"
	"os"
	"os/exec"

	"github.com/creack/pty"
)

// RealPTYFactory creates real PTY processes using creack/pty.
type RealPTYFactory struct{}

func (f *RealPTYFactory) Start(command []string, dir string, env []string, cols, rows int) (PTY, Process, error) {
	cmd := exec.Command(command[0], command[1:]...)
	cmd.Env = env
	if dir != "" {
		cmd.Dir = dir
	}

	ptmx, err := pty.StartWithSize(cmd, &pty.Winsize{
		Cols: uint16(cols),
		Rows: uint16(rows),
	})
	if err != nil {
		return nil, nil, err
	}

	return &realPTY{ptmx: ptmx}, &realProcess{cmd: cmd}, nil
}

type realPTY struct {
	ptmx *os.File
}

func (p *realPTY) Read(b []byte) (int, error)  { return p.ptmx.Read(b) }
func (p *realPTY) Write(b []byte) (int, error) { return p.ptmx.Write(b) }
func (p *realPTY) Resize(cols, rows int) error {
	return pty.Setsize(p.ptmx, &pty.Winsize{Cols: uint16(cols), Rows: uint16(rows)})
}
func (p *realPTY) Close() error { return p.ptmx.Close() }

type realProcess struct {
	cmd *exec.Cmd
}

func (p *realProcess) Wait() (int, error) {
	if err := p.cmd.Wait(); err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return exitErr.ExitCode(), nil
		}
		return -1, err
	}
	return 0, nil
}

func (p *realProcess) Signal(sig os.Signal) error {
	if p.cmd.Process == nil {
		return nil
	}
	return p.cmd.Process.Signal(sig)
}

// RealOSEnv delegates to the os package.
type RealOSEnv struct{}

func (e *RealOSEnv) Environ() []string                  { return os.Environ() }
func (e *RealOSEnv) Stat(path string) (os.FileInfo, error) { return os.Stat(path) }
