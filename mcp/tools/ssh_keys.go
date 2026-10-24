package tools

import (
	"context"
	"net/http"
	"strings"

	pantech "github.com/Pantech-Dynamics/pantech-go"
)

type createSSHKeyInput struct {
	Name      string `json:"name" jsonschema:"1 to 255 characters, unique among your SSH keys."`
	PublicKey string `json:"public_key" jsonschema:"One OpenSSH public key line, e.g. ssh-ed25519 AAAA… user@host. Never a private key."`
}

func init() {
	add(Spec{
		Name: "list_ssh_keys", Title: "List SSH keys",
		Description: "List the SSH public keys on the account. An id is the ssh_key_id for create_instance and change_instance_ssh_key.",
		Risk:        Read, Idempotent: true,
		Routes: []string{"GET /ssh-keys"},
	}, func(ctx context.Context, c *Call, _ noInput) (*Result, error) {
		keys, err := c.Client.SSHKeys.List(ctx)
		if err != nil {
			return nil, err
		}
		return ok(plural(len(keys), "SSH key", "SSH keys")+".", keys)
	})

	// Only importing a public key is offered: letting the API generate a
	// pair would put the private key into the conversation.
	add(Spec{
		Name: "create_ssh_key", Title: "Add an SSH public key",
		Description: "Register an OpenSSH public key the user already has (public_key is required). This tool never generates key pairs: ask the user for their public key (e.g. the contents of ~/.ssh/id_ed25519.pub).",
		Risk:        Write,
		Routes:      []string{"POST /ssh-keys"},
	}, func(ctx context.Context, c *Call, in createSSHKeyInput) (*Result, error) {
		pub := strings.TrimSpace(in.PublicKey)
		if pub == "" {
			return nil, errorf("public_key is required: ask the user for their OpenSSH public key.")
		}
		if strings.Contains(pub, "PRIVATE KEY") {
			return nil, errorf("That is a private key. Never share it: ask the user for the public key (the .pub file) instead.")
		}
		// Do, not the typed create: decoding into SSHKey drops any private_key
		// field, so a secret could never reach the result.
		var key pantech.SSHKey
		body := pantech.CreateSSHKeyRequest{Name: in.Name, PublicKey: pantech.Ptr(pub)}
		if err := c.Client.Do(ctx, http.MethodPost, "/ssh-keys", nil, body, &key, c.writeOpts()...); err != nil {
			return nil, err
		}
		return ok("Added SSH key "+key.ID+" (fingerprint "+key.Fingerprint+").", key)
	})
}
