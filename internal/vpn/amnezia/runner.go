package amnezia

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log"
	"os/exec"
	"slices"
	"strings"
	"time"

	"github.com/irbgeo/geoirb-vpn-bot/internal/config"
)

// containerNames in order of preference: new installs first, old ones second.
var containerNames = []string{
	"amnezia-awg2",
	"amnezia-awg",
}

// DockerRunner runs commands inside a container with `docker exec`.
type DockerRunner struct {
	Bin       string // docker binary, "docker" by default
	Container string
	Timeout   time.Duration
}

// NewDockerRunner creates a DockerRunner for the Amnezia container: the
// one in AWG_CONTAINER, or the one found by DetectContainer.
func NewDockerRunner(
	ctx context.Context,
	cfg *config.Config,
) (*DockerRunner, error) {
	name := cfg.AWGContainer
	if name == "" {
		var err error
		name, err = DetectContainer(ctx, cfg.DockerBin)
		if err != nil {
			return nil, err
		}
	}
	log.Printf("vpn: container %s", name)
	return &DockerRunner{
		Bin:       cfg.DockerBin,
		Container: name,
		Timeout:   cfg.DockerTimeout,
	}, nil
}

// DetectContainer finds the running Amnezia AWG container via `docker ps`.
func DetectContainer(ctx context.Context, bin string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	out, err := exec.CommandContext(ctx, bin, "ps", "--format", "{{.Names}}").Output()
	if err != nil {
		return "", fmt.Errorf("amnezia: docker ps: %w", err)
	}
	running := strings.Fields(string(out))
	for _, want := range containerNames {
		if slices.Contains(running, want) {
			return want, nil
		}
	}
	return "", errors.New("amnezia: no amnezia-awg2 or amnezia-awg container is running")
}

// Exec runs one command in the container and returns its stdout.
// Errors carry the command and stderr, never stdin (it may hold secrets).
func (s *DockerRunner) Exec(ctx context.Context, in execInput) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, s.Timeout)
	defer cancel()

	args := append(
		[]string{
			"exec",
			"-i",
			s.Container,
		},
		in.Args...,
	)
	cmd := exec.CommandContext(ctx, s.bin(), args...)
	cmd.Stdin = strings.NewReader(in.Stdin)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	// On timeout only the docker CLI is killed: the command inside the
	// container may still finish, so callers must treat the result as unknown.
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return "", fmt.Errorf("amnezia: %s: timed out after %s", strings.Join(in.Args, " "), s.Timeout)
	}
	if err != nil {
		return "", fmt.Errorf("amnezia: %s: %w: %s", strings.Join(in.Args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return stdout.String(), nil
}

func (s *DockerRunner) bin() string {
	if s.Bin == "" {
		return "docker"
	}
	return s.Bin
}
