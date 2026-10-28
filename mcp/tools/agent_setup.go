package tools

import (
	"context"
	"fmt"

	pantech "github.com/Pantech-Dynamics/pantech-go"
)

type enableInstanceAgentInput struct {
	InstanceID string `json:"instance_id" jsonschema:"The instance's id, e.g. vm_…. It must be running."`
	Confirmation
}

func init() {
	addConfirmed(Spec{
		Name: "enable_instance_agent", Title: "Set up an instance's Pantech agent",
		Description: "Set the Pantech agent up on a running instance, so tools sessions can work there. The platform restarts the instance once (about two minutes of downtime) and the agent installs itself and connects a few minutes later; nobody runs commands. Only an owner or admin's connection can do this. Afterwards, get_instance_agent shows when it is connected.",
		Risk:        Destructive, Destructive: true, Idempotent: true,
		Routes: []string{"GET /instances/{instance_id}", "GET /instances/{instance_id}/agent", "POST /instances/{instance_id}/agent/setup", "GET /operations/{operation_id}"},
	}, func(ctx context.Context, c *Call, in enableInstanceAgentInput) (*Preview, error) {
		if err := checkIDs("instance_id", in.InstanceID); err != nil {
			return nil, err
		}
		vm, err := c.Client.Instances.Get(ctx, in.InstanceID)
		if err != nil {
			return nil, err
		}
		var h agentHost
		if err := c.Client.Do(ctx, "GET", "/instances/"+in.InstanceID+"/agent", nil, nil, &h); err != nil {
			return nil, err
		}
		if h.Enrolled {
			return nil, errorf("Instance %s's agent is already set up: use start_tools_session.", in.InstanceID)
		}
		return &Preview{
			Action: fmt.Sprintf("Set up the Pantech agent on instance %s. The instance restarts once (about two minutes of downtime), then the agent installs itself and connects within a few minutes.", vm.ID),
			Data:   map[string]any{"instance": vm, "agent": h},
		}, nil
	}, func(ctx context.Context, c *Call, in enableInstanceAgentInput) (*Result, error) {
		var acc pantech.Accepted
		if err := c.Client.Do(ctx, "POST", "/instances/"+in.InstanceID+"/agent/setup", nil, nil, &acc, c.writeOpts()...); err != nil {
			return nil, err
		}
		return c.finish(ctx, &acc, fmt.Sprintf("instance %s restarted to set its agent up; it connects within a few minutes (check with get_instance_agent)", in.InstanceID))
	})
}
