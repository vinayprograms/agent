package main

import (
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/alecthomas/kong"
	"github.com/nats-io/nats.go"
	"github.com/vinayprograms/agentkit/registry"
)

type CLI struct {
	NATSURL string `name:"nats" help:"NATS server URL" default:"nats://localhost:4222"`

	Status       StatusCmd       `cmd:"" help:"Show swarm status"`
	Agents       AgentsCmd       `cmd:"" help:"List registered agents"`
	Capabilities CapabilitiesCmd `cmd:"" help:"List capabilities in swarm"`
}

type StatusCmd struct{}
type AgentsCmd struct{}
type CapabilitiesCmd struct{}

func main() {
	cli := &CLI{}
	ctx := kong.Parse(cli,
		kong.Name("swarm"),
		kong.Description("Personal swarm controller"),
	)

	err := ctx.Run(&runContext{natsURL: cli.NATSURL})
	ctx.FatalIfErrorf(err)
}

type runContext struct {
	natsURL string
}

func (c *runContext) withRegistry(fn func(reg registry.Registry) error) error {
	nc, err := nats.Connect(c.natsURL)
	if err != nil {
		return fmt.Errorf("connect nats: %w", err)
	}
	defer nc.Close()

	reg, err := registry.NewNATSRegistry(nc, registry.DefaultNATSRegistryConfig())
	if err != nil {
		return fmt.Errorf("create registry: %w", err)
	}
	defer reg.Close()

	return fn(reg)
}

func (s *StatusCmd) Run(rc *runContext) error {
	return rc.withRegistry(func(reg registry.Registry) error {
		agents, err := reg.List(nil)
		if err != nil {
			return err
		}

		var idle, busy, running, stopping int
		capSet := map[string]struct{}{}
		now := time.Now()
		for _, a := range agents {
			switch a.Status {
			case registry.StatusIdle:
				idle++
			case registry.StatusBusy:
				busy++
			case registry.StatusRunning:
				running++
			case registry.StatusStopping:
				stopping++
			}
			for _, cap := range a.Capabilities {
				capSet[cap] = struct{}{}
			}
			_ = now.Sub(a.LastSeen)
		}

		fmt.Printf("NATS: %s\n", rc.natsURL)
		fmt.Printf("Agents: %d (idle=%d busy=%d running=%d stopping=%d)\n", len(agents), idle, busy, running, stopping)
		fmt.Printf("Capabilities: %d\n", len(capSet))
		return nil
	})
}

func (a *AgentsCmd) Run(rc *runContext) error {
	return rc.withRegistry(func(reg registry.Registry) error {
		agents, err := reg.List(nil)
		if err != nil {
			return err
		}
		sort.Slice(agents, func(i, j int) bool { return agents[i].ID < agents[j].ID })

		if len(agents) == 0 {
			fmt.Println("No agents registered")
			return nil
		}

		for _, ag := range agents {
			caps := strings.Join(ag.Capabilities, ",")
			age := time.Since(ag.LastSeen).Round(time.Second)
			fmt.Printf("%s\t%s\tload=%.2f\tlast_seen=%s\tcaps=%s\n", ag.ID, ag.Status, ag.Load, age, caps)
		}
		return nil
	})
}

func (c *CapabilitiesCmd) Run(rc *runContext) error {
	return rc.withRegistry(func(reg registry.Registry) error {
		agents, err := reg.List(nil)
		if err != nil {
			return err
		}

		counts := map[string]int{}
		for _, ag := range agents {
			for _, cap := range ag.Capabilities {
				counts[cap]++
			}
		}

		if len(counts) == 0 {
			fmt.Println("No capabilities found")
			return nil
		}

		caps := make([]string, 0, len(counts))
		for cap := range counts {
			caps = append(caps, cap)
		}
		sort.Strings(caps)
		for _, cap := range caps {
			fmt.Printf("%s\t(%d agents)\n", cap, counts[cap])
		}
		return nil
	})
}

func init() {
	if os.Getenv("SWARM_DEBUG") != "" {
		fmt.Fprintln(os.Stderr, "swarm debug enabled")
	}
}
