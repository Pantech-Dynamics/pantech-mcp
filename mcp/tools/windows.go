package tools

import (
	"context"
	"fmt"
	"net/url"

	pantech "github.com/Pantech-Dynamics/pantech-go"
)

// Windows Server instances sign in over Remote Desktop as Administrator with
// a password the platform generates. The password is shown once, in the
// console, and never through this server: like every other secret it stays
// out of the AI app's transcript (see excluded.go).

const rdpPort = 3389

// imageFamily is the part of an image the SDK does not carry yet.
type imageFamily struct {
	Slug        string `json:"slug"`
	OSFamily    string `json:"os_family"`
	MinVCPU     int    `json:"min_vcpu"`
	MinMemoryMB int    `json:"min_memory_mb"`
}

func (i *imageFamily) windows() bool { return i != nil && i.OSFamily == "windows" }

// findImage looks an image up by slug; nil when the catalogue does not list it.
func (c *Call) findImage(ctx context.Context, slug string) (*imageFamily, error) {
	cursor := ""
	for range 20 {
		q := url.Values{"limit": {"100"}}
		if cursor != "" {
			q.Set("cursor", cursor)
		}
		var page pantech.Page[imageFamily]
		if err := c.Client.Do(ctx, "GET", "/images", q, nil, &page); err != nil {
			return nil, err
		}
		for i := range page.Data {
			if page.Data[i].Slug == slug {
				return &page.Data[i], nil
			}
		}
		if !page.HasMore() {
			return nil, nil
		}
		cursor = *page.NextCursor
	}
	return nil, nil
}

// checkImageAccess applies an image's sign-in rules to a create: a Windows
// image takes no SSH key and no agent and has a minimum size; any other image
// needs a key.
func checkImageAccess(img *imageFamily, in createInstanceInput, plan *pantech.Plan) error {
	if !img.windows() {
		if in.SSHKeyID == "" {
			return errorf("ssh_key_id is required for image %s. Call list_ssh_keys, or create_ssh_key with the user's public key.", arg(in.ImageSlug))
		}
		return nil
	}
	if in.SSHKeyID != "" {
		return errorf("Image %s is Windows Server: it signs in over Remote Desktop with a generated password, not an SSH key. Leave ssh_key_id out.", arg(in.ImageSlug))
	}
	if in.Agent {
		return errorf("The Pantech agent runs on Linux only; leave agent out for Windows image %s.", arg(in.ImageSlug))
	}
	if plan.VCPU < img.MinVCPU || plan.MemoryMB < img.MinMemoryMB {
		return errorf("Image %s needs a plan with at least %d vCPU and %d MB memory; %s has %d vCPU and %d MB. Call list_plans for a bigger one.",
			arg(in.ImageSlug), img.MinVCPU, img.MinMemoryMB, plan.Slug, plan.VCPU, plan.MemoryMB)
	}
	return nil
}

// windowsNote is what a Windows create's preview tells the user about signing in.
func windowsNote(vpc bool) string {
	s := fmt.Sprintf("Windows Server: sign in over Remote Desktop (port %d) as Administrator. The password is generated at creation and shown once, on the instance's RDP tab in the console; if nobody looks at it, it is deleted after 7 days.", rdpPort)
	if vpc {
		s += fmt.Sprintf(" In a VPC it needs a public IP with a port forward for %d, and the subnet's ACL must allow %d from the user's address.", rdpPort, rdpPort)
	} else {
		s += fmt.Sprintf(" Its security group must allow TCP %d from the user's address (add_firewall_rule), ideally that address only.", rdpPort)
	}
	return s
}

type remoteDesktopInstance struct {
	ID            string  `json:"id"`
	Name          string  `json:"name"`
	OSFamily      string  `json:"os_family"`
	ObservedState string  `json:"observed_state"`
	PublicIPv4    *string `json:"public_ipv4"`
	PrivateIPv4   *string `json:"private_ipv4"`
	SubnetID      *string `json:"subnet_id"`
}

func init() {
	add(Spec{
		Name: "get_remote_desktop", Title: "Get Remote Desktop details",
		Description: "How to connect to a Windows Server instance over Remote Desktop: the address and port, the Administrator user, and where the user sees the one-time password (the console; it never passes through this tool). For a Linux instance, use get_ssh_access instead.",
		Risk:        Read, Idempotent: true,
		Routes: []string{"GET /instances/{instance_id}"},
	}, func(ctx context.Context, c *Call, in instanceInput) (*Result, error) {
		if err := checkIDs("instance_id", in.InstanceID); err != nil {
			return nil, err
		}
		var vm remoteDesktopInstance
		if err := c.Client.Do(ctx, "GET", "/instances/"+url.PathEscape(in.InstanceID), nil, nil, &vm); err != nil {
			return nil, err
		}
		if vm.OSFamily != "windows" {
			return nil, errorf("Instance %s is not Windows Server; it signs in with SSH. Use get_ssh_access or open_ssh_access.", vm.ID)
		}
		vpc := vm.SubnetID != nil && *vm.SubnetID != ""
		address := ""
		if vm.PublicIPv4 != nil {
			address = *vm.PublicIPv4
		}
		text := fmt.Sprintf("Instance %s (%s) is %s. Remote Desktop user: Administrator.", vm.ID, vm.Name, vm.ObservedState)
		switch {
		case address != "":
			text += fmt.Sprintf(" Connect to %s:%d.", address, rdpPort)
		case vpc:
			text += fmt.Sprintf(" It is in a VPC with no public address of its own: attach a public IP and add a port forward for %d (create_port_forwarding_rule), then connect to that IP.", rdpPort)
		default:
			text += " It has no public address yet."
		}
		text += " The password is shown once, on the instance's RDP tab in the console; this tool cannot read it."
		return ok(text, map[string]any{
			"instance_id": vm.ID, "username": "Administrator", "address": address, "port": rdpPort, "in_vpc": vpc,
		})
	})
}
