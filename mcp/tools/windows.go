package tools

import (
	"context"
	"fmt"
	"net/url"

	pantech "github.com/Pantech-Dynamics/pantech-go"
)

// Windows Server instances sign in over Remote Desktop as Administrator with
// a password the user chooses when they create the instance, and can reset
// later. Both happen in the console, never through this server: like every
// other secret the password stays out of the AI app's transcript (see
// excluded.go). So a Windows image cannot be created here at all, since the
// API needs that password with the order.

const rdpPort = 3389

// imageFamily is the part of an image the SDK does not carry yet.
type imageFamily struct {
	Slug     string `json:"slug"`
	OSFamily string `json:"os_family"`
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
// image is created in the console, where the user types its password; any
// other image needs a key.
func checkImageAccess(img *imageFamily, in createInstanceInput) error {
	if img.windows() {
		return errorf("Image %s is Windows Server, which signs in with an Administrator password the user types when creating it. Passwords never pass through this server, so ask the user to create it in the console (Virtual machines > Create). Once it exists, get_remote_desktop gives its address.", arg(in.ImageSlug))
	}
	if in.SSHKeyID == "" {
		return errorf("ssh_key_id is required for image %s. Call list_ssh_keys, or create_ssh_key with the user's public key.", arg(in.ImageSlug))
	}
	return nil
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
		Description: "How to connect to a Windows Server instance over Remote Desktop: the address and port and the Administrator user. The password is the one the user chose when creating it; it never passes through this tool, and the user resets it in the console. For a Linux instance, use get_ssh_access instead.",
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
		text += " The password is the one the user chose when creating it; this tool cannot read it. A forgotten one is reset in the console, on the instance's RDP tab."
		return ok(text, map[string]any{
			"instance_id": vm.ID, "username": "Administrator", "address": address, "port": rdpPort, "in_vpc": vpc,
		})
	})
}
