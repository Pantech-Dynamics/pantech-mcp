package tools

import (
	"context"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"time"

	pantech "github.com/Pantech-Dynamics/pantech-go"
)

// Pantech Container Registry holds the organization's container images at
// registry.pantechdynamics.com/<namespace>/<repository>:<tag>. These tools
// manage the namespace, repositories, tags and registry tokens; images
// themselves are pushed and pulled with docker or podman, never through
// this server. Descriptions and tag names are the customer's own text, so
// they go only in the fenced data. A new registry token's secret is shown
// once, in the fenced data only, never in a summary or a preview.

type registryNamespace struct {
	Name              string `json:"name"`
	RegistryHost      string `json:"registry_host"`
	CreateOnPush      bool   `json:"create_on_push"`
	RepositoryCount   int    `json:"repository_count"`
	StorageBytes      int64  `json:"storage_bytes"`
	StorageLimitBytes int64  `json:"storage_limit_bytes"`
	RepositoryLimit   int    `json:"repository_limit"`
	CreatedAt         string `json:"created_at"`
	UpdatedAt         string `json:"updated_at"`
	Version           int64  `json:"version"`
}

// registryRetention is a repository's retention policy. Each rule is null
// when it is not set, and is sent as null to remove it.
type registryRetention struct {
	KeepLast          *int    `json:"keep_last" jsonschema:"Keep only the newest N tags, 1 to 1000; older ones are deleted daily. Leave out for no limit."`
	UntaggedAfterDays *int    `json:"untagged_after_days" jsonschema:"Delete untagged images older than this many days, 1 to 365. Leave out to keep them."`
	KeepPattern       *string `json:"keep_pattern" jsonschema:"Tags matching this glob, e.g. v* or release-*, are never removed by retention. At most 128 characters."`
}

type registryRepository struct {
	ID              string             `json:"id"`
	Namespace       string             `json:"namespace"`
	Name            string             `json:"name"`
	FullName        string             `json:"full_name"`
	Description     string             `json:"description"`
	Visibility      string             `json:"visibility"`
	ImmutableTags   bool               `json:"immutable_tags"`
	Retention       *registryRetention `json:"retention"`
	TagCount        int                `json:"tag_count"`
	StorageBytes    int64              `json:"storage_bytes"`
	LastPushedAt    *string            `json:"last_pushed_at"`
	Status          string             `json:"status"`
	SuspendedReason *string            `json:"suspended_reason"`
	CreatedAt       string             `json:"created_at"`
	UpdatedAt       string             `json:"updated_at"`
	Version         int64              `json:"version"`
}

type registryTag struct {
	Name         string   `json:"name"`
	Digest       string   `json:"digest"`
	MediaType    string   `json:"media_type"`
	SizeBytes    int64    `json:"size_bytes"`
	Platforms    []string `json:"platforms"`
	PushedAt     string   `json:"pushed_at"`
	LastPulledAt *string  `json:"last_pulled_at"`
	Immutable    bool     `json:"immutable"`
}

type registryToken struct {
	ID           string   `json:"id"`
	Name         string   `json:"name"`
	Permission   string   `json:"permission"`
	Repositories []string `json:"repositories"`
	ExpiresAt    *string  `json:"expires_at"`
	LastUsedAt   *string  `json:"last_used_at"`
	CreatedAt    string   `json:"created_at"`
	RevokedAt    *string  `json:"revoked_at"`
	// Secret is set only in the answer to a create.
	Secret string `json:"secret,omitempty"`
}

var (
	// namespacePattern is 3 to 30 lower-case letters, digits and single
	// hyphens, starting with a letter (the length is checked separately).
	namespacePattern = regexp.MustCompile(`^[a-z][a-z0-9]*(?:-[a-z0-9]+)*$`)
	// repositoryPattern is the OCI name grammar: lower-case path
	// components separated by "/".
	repositoryPattern = regexp.MustCompile(`^[a-z0-9]+(?:[._-][a-z0-9]+)*(?:/[a-z0-9]+(?:[._-][a-z0-9]+)*)*$`)
	// tagPattern is the OCI tag grammar. It holds no "/", "?" or "%", so a
	// tag is safe in a URL path as is.
	tagPattern = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9._-]{0,127}$`)
)

const registryHost = "registry.pantechdynamics.com"

func checkNamespace(name string) error {
	if len(name) < 3 || len(name) > 30 || !namespacePattern.MatchString(name) {
		return errorf("name must be 3 to 30 lower-case letters, digits and single hyphens, starting with a letter, e.g. acme.")
	}
	return nil
}

func checkRepositoryName(name string) error {
	if len(name) > 255 || !repositoryPattern.MatchString(name) {
		return errorf("name must be lower-case letters and digits, with single . _ or - between them and / between parts, e.g. api/server.")
	}
	return nil
}

func checkTag(tag string) error {
	if !tagPattern.MatchString(tag) {
		return errorf("tag must be a tag name exactly as list_registry_tags returned it, e.g. v1.4.2.")
	}
	return nil
}

func checkVisibility(v string) error {
	if v != "" && v != "private" && v != "public" {
		return errorf("visibility must be private or public.")
	}
	return nil
}

func (r *registryRetention) check() error {
	if r == nil {
		return nil
	}
	if r.KeepLast != nil && (*r.KeepLast < 1 || *r.KeepLast > 1000) {
		return errorf("retention.keep_last must be from 1 to 1000.")
	}
	if r.UntaggedAfterDays != nil && (*r.UntaggedAfterDays < 1 || *r.UntaggedAfterDays > 365) {
		return errorf("retention.untagged_after_days must be from 1 to 365.")
	}
	if r.KeepPattern != nil && (*r.KeepPattern == "" || len(*r.KeepPattern) > 128) {
		return errorf("retention.keep_pattern must be a glob of 1 to 128 characters, e.g. v*.")
	}
	return nil
}

// gib formats a size in GiB with two decimals: storage is shown for
// orientation, it is not an amount of money.
func gib(bytes int64) string {
	return fmt.Sprintf("%.2f GiB", float64(bytes)/(1<<30))
}

func namespaceText(ns *registryNamespace) string {
	text := fmt.Sprintf("The organization's registry namespace is %s: images are %s/%s/<repository>:<tag>. %s, %s of %s used.",
		arg(ns.Name), registryHost, arg(ns.Name), plural(ns.RepositoryCount, "repository", "repositories"), gib(ns.StorageBytes), gib(ns.StorageLimitBytes))
	if ns.CreateOnPush {
		text += " Pushing to a new repository creates it."
	}
	return text + " Log in with: docker login " + registryHost + " -u pantech (the password is a Pantech API key or a registry token)."
}

func repositoryText(r *registryRepository) string {
	text := fmt.Sprintf("Repository %s (%s/%s/%s) is %s and %s: %s, %s.", arg(r.ID), registryHost, arg(r.Namespace), arg(r.Name),
		arg(r.Status), arg(r.Visibility), plural(r.TagCount, "tag", "tags"), gib(r.StorageBytes))
	if r.Visibility == "public" {
		text += " Anyone can pull its images without logging in."
	}
	if r.ImmutableTags {
		text += " Its tags are immutable: a pushed tag cannot be moved or deleted."
	}
	return text
}

type registryNamespaceInput struct {
	Name         string `json:"name" jsonschema:"The namespace, 3 to 30 lower-case letters, digits and single hyphens, starting with a letter, e.g. acme. It is part of every image name and cannot change once a repository has ever been created in it."`
	CreateOnPush *bool  `json:"create_on_push,omitempty" jsonschema:"Whether pushing to a repository that does not exist creates it. Default true."`
	Confirmation
}

type listRegistryRepositoriesInput struct {
	Q string `json:"q,omitempty" jsonschema:"Only repositories whose name contains this text."`
	pageInput
}

type createRegistryRepositoryInput struct {
	Name          string             `json:"name" jsonschema:"The repository's name in the namespace, e.g. api/server: lower-case letters and digits, with single . _ or - between them and / between parts."`
	Description   string             `json:"description,omitempty" jsonschema:"A note for the team."`
	Visibility    string             `json:"visibility,omitempty" jsonschema:"private (default; pulls need credentials) or public (anyone can pull without logging in)."`
	ImmutableTags bool               `json:"immutable_tags,omitempty" jsonschema:"true: a tag, once pushed, cannot be moved or deleted."`
	Retention     *registryRetention `json:"retention,omitempty" jsonschema:"Rules that delete old images daily. Leave out to keep everything."`
	Confirmation
}

type registryRepositoryInput struct {
	RepositoryID string `json:"repository_id" jsonschema:"The repository's id, e.g. crr_…, from list_registry_repositories."`
}

type updateRegistryRepositoryInput struct {
	RepositoryID  string             `json:"repository_id" jsonschema:"The repository's id, e.g. crr_…"`
	Description   *string            `json:"description,omitempty" jsonschema:"A new note for the team."`
	Visibility    string             `json:"visibility,omitempty" jsonschema:"private or public (anyone can pull without logging in)."`
	ImmutableTags *bool              `json:"immutable_tags,omitempty" jsonschema:"true: pushed tags cannot be moved or deleted. false allows that again."`
	Retention     *registryRetention `json:"retention,omitempty" jsonschema:"The whole new retention policy: a rule left out is removed. Retention deletes images daily."`
	Confirmation
}

type deleteRegistryRepositoryInput struct {
	RepositoryID string `json:"repository_id" jsonschema:"The repository's id, e.g. crr_…"`
	Confirmation
}

type listRegistryTagsInput struct {
	RepositoryID string `json:"repository_id" jsonschema:"The repository's id, e.g. crr_…"`
	Q            string `json:"q,omitempty" jsonschema:"Only tags whose name contains this text."`
	pageInput
}

type registryTagInput struct {
	RepositoryID string `json:"repository_id" jsonschema:"The repository's id, e.g. crr_…"`
	Tag          string `json:"tag" jsonschema:"The tag, e.g. v1.4.2, from list_registry_tags."`
}

type deleteRegistryTagInput struct {
	RepositoryID string `json:"repository_id" jsonschema:"The repository's id, e.g. crr_…"`
	Tag          string `json:"tag" jsonschema:"The tag, e.g. v1.4.2."`
	Confirmation
}

type createRegistryTokenInput struct {
	Name         string   `json:"name" jsonschema:"A name saying where the token is used, e.g. github-actions or prod-cluster."`
	Permission   string   `json:"permission" jsonschema:"pull (read images only, e.g. for a cluster) or push (push and pull, e.g. for CI)."`
	Repositories []string `json:"repositories,omitempty" jsonschema:"Repository names in the namespace it works for, e.g. api/server. Leave out for every repository."`
	ExpiresAt    string   `json:"expires_at,omitempty" jsonschema:"When it stops working, RFC 3339, e.g. 2027-11-10T00:00:00Z. Leave out for no expiry."`
	Confirmation
}

type revokeRegistryTokenInput struct {
	TokenID string `json:"token_id" jsonschema:"The registry token's id, e.g. prt_…, from list_registry_tokens."`
	Confirmation
}

func repositoryPath(id string) string { return "/registry/repositories/" + id }

func init() {
	add(Spec{
		Name: "get_registry_namespace", Title: "Get the registry namespace",
		Description: "Get the organization's container registry namespace: the prefix of every image name (registry.pantechdynamics.com/<namespace>/<repository>:<tag>), whether a push creates repositories, and repository and storage use against the limits. Fails with REGISTRY_NAMESPACE_NOT_SET until one is claimed with set_registry_namespace.",
		Risk:        Read, Idempotent: true,
		Routes: []string{"GET /registry"},
	}, func(ctx context.Context, c *Call, _ noInput) (*Result, error) {
		var ns registryNamespace
		if err := c.Client.Do(ctx, "GET", "/registry", nil, nil, &ns); err != nil {
			return nil, err
		}
		return ok(namespaceText(&ns), ns)
	})

	addConfirmed(Spec{
		Name: "set_registry_namespace", Title: "Claim or change the registry namespace",
		Description: "Claim the organization's container registry namespace, or change it or create_on_push. The name is globally unique, is part of every image name, and cannot change once a repository has ever been created in it. Owners and admins only.",
		Risk:        Destructive, Destructive: true, Idempotent: true,
		Routes: []string{"GET /registry", "PUT /registry"},
	}, func(ctx context.Context, c *Call, in registryNamespaceInput) (*Preview, error) {
		if err := checkNamespace(in.Name); err != nil {
			return nil, err
		}
		p := &Preview{
			Action: fmt.Sprintf("Set the organization's registry namespace to %s: images will be %s/%s/<repository>:<tag>.", in.Name, registryHost, in.Name),
			Warnings: []string{
				"The namespace cannot be changed once a repository has ever been created in it, and it is seen by everyone who pulls the images.",
			},
			Data: map[string]any{"request": in},
		}
		var ns registryNamespace
		if err := c.Client.Do(ctx, "GET", "/registry", nil, nil, &ns); err == nil {
			p.Data = map[string]any{"current": ns, "request": in}
			if ns.Name != "" && ns.Name != in.Name && ns.RepositoryCount > 0 {
				p.Warnings = append(p.Warnings, "The namespace already has repositories, so Pantech refuses to rename it (REGISTRY_NAMESPACE_LOCKED).")
			}
		} else if e, isAPI := pantech.AsError(err); !isAPI || e.Code != "REGISTRY_NAMESPACE_NOT_SET" {
			return nil, err
		}
		return p, nil
	}, func(ctx context.Context, c *Call, in registryNamespaceInput) (*Result, error) {
		body := map[string]any{"name": in.Name}
		if in.CreateOnPush != nil {
			body["create_on_push"] = *in.CreateOnPush
		}
		var ns registryNamespace
		if err := c.Client.Do(ctx, "PUT", "/registry", nil, body, &ns, c.writeOpts()...); err != nil {
			return nil, err
		}
		return ok(namespaceText(&ns), ns)
	})

	add(Spec{
		Name: "list_registry_repositories", Title: "List container repositories",
		Description: "List the namespace's container repositories with visibility, immutable tags, retention, tag count, storage and last push. Paginated.",
		Risk:        Read, Idempotent: true,
		Routes: []string{"GET /registry/repositories"},
	}, func(ctx context.Context, c *Call, in listRegistryRepositoriesInput) (*Result, error) {
		q := pageQuery(in.pageInput)
		setIf(q, "q", in.Q)
		var page pantech.Page[registryRepository]
		if err := c.Client.Do(ctx, "GET", "/registry/repositories", q, nil, &page); err != nil {
			return nil, err
		}
		return pageResult(&page, "repository", "repositories")
	})

	addConfirmedWith(Spec{
		Name: "create_registry_repository", Title: "Create a container repository",
		Description: "Create a repository in the namespace, private by default. A push to a new name also creates one when the namespace's create_on_push is on. Owners and admins only; at most 100 repositories.",
		Risk:        Exposure, Idempotent: true,
		ConfirmWhen: "when visibility is public",
		Routes:      []string{"POST /registry/repositories"},
	}, confirmOptions[createRegistryRepositoryInput]{
		when: func(in createRegistryRepositoryInput) bool { return in.Visibility == "public" },
	}, func(ctx context.Context, c *Call, in createRegistryRepositoryInput) (*Preview, error) {
		if err := checkCreateRepository(in); err != nil {
			return nil, err
		}
		return &Preview{
			Action: fmt.Sprintf("Create the public repository %s: anyone on the internet can pull every image pushed to it, without logging in.", in.Name),
			Data:   map[string]any{"request": in},
		}, nil
	}, func(ctx context.Context, c *Call, in createRegistryRepositoryInput) (*Result, error) {
		if err := checkCreateRepository(in); err != nil {
			return nil, err
		}
		body := map[string]any{"name": in.Name}
		if in.Description != "" {
			body["description"] = in.Description
		}
		if in.Visibility != "" {
			body["visibility"] = in.Visibility
		}
		if in.ImmutableTags {
			body["immutable_tags"] = true
		}
		if in.Retention != nil {
			body["retention"] = in.Retention
		}
		var r registryRepository
		if err := c.Client.Do(ctx, "POST", "/registry/repositories", nil, body, &r, c.writeOpts()...); err != nil {
			return nil, err
		}
		return ok("Created. "+repositoryText(&r)+" Push with: docker push "+registryHost+"/"+arg(r.Namespace)+"/"+arg(r.Name)+":<tag>.", r)
	})

	add(Spec{
		Name: "get_registry_repository", Title: "Get a container repository",
		Description: "Get a repository: its full image name, visibility, immutable tags, retention policy, tag count, storage and last push.",
		Risk:        Read, Idempotent: true,
		Routes: []string{"GET /registry/repositories/{repository_id}"},
	}, func(ctx context.Context, c *Call, in registryRepositoryInput) (*Result, error) {
		if err := checkIDs("repository_id", in.RepositoryID); err != nil {
			return nil, err
		}
		r, err := c.getRepository(ctx, in.RepositoryID)
		if err != nil {
			return nil, err
		}
		return ok(repositoryText(r), r)
	})

	addConfirmedWith(Spec{
		Name: "update_registry_repository", Title: "Change a container repository",
		Description: "Change a repository's description, visibility, immutable tags or retention policy. Making it public lets anyone pull its images; turning immutable tags off lets tags be moved and deleted; a retention policy deletes old images daily.",
		Risk:        Destructive, Destructive: true, Idempotent: true,
		ConfirmWhen: "when it makes the repository public, turns immutable tags off or sets a retention policy",
		Routes:      []string{"GET /registry/repositories/{repository_id}", "PATCH /registry/repositories/{repository_id}"},
	}, confirmOptions[updateRegistryRepositoryInput]{
		when: repositoryChangeNeedsConfirmation,
	}, func(ctx context.Context, c *Call, in updateRegistryRepositoryInput) (*Preview, error) {
		body, err := repositoryChange(in)
		if err != nil {
			return nil, err
		}
		r, err := c.getRepository(ctx, in.RepositoryID)
		if err != nil {
			return nil, err
		}
		var notes []string
		if in.Visibility == "public" {
			notes = append(notes, "Public: anyone on the internet can pull every image in it, without logging in. Check the images hold no secrets.")
		}
		if isFalse(in.ImmutableTags) {
			notes = append(notes, "With immutable tags off, anyone with push access can move a tag to another image or delete it.")
		}
		if in.Retention != nil {
			notes = append(notes, fmt.Sprintf("Retention runs daily and deletes the images it selects; the repository has %s now. A rule left out of the new policy is removed.", plural(r.TagCount, "tag", "tags")))
		}
		return &Preview{
			Action:   fmt.Sprintf("Change repository %s (the change is in the data).", r.ID),
			Warnings: notes,
			Data:     map[string]any{"current": r, "change": body},
		}, nil
	}, func(ctx context.Context, c *Call, in updateRegistryRepositoryInput) (*Result, error) {
		body, err := repositoryChange(in)
		if err != nil {
			return nil, err
		}
		var r registryRepository
		if err := c.Client.Do(ctx, "PATCH", repositoryPath(in.RepositoryID), nil, body, &r, c.writeOpts()...); err != nil {
			return nil, err
		}
		return ok("Saved. "+repositoryText(&r), r)
	})

	addConfirmed(Spec{
		Name: "delete_registry_repository", Title: "Delete a container repository",
		Description: "Delete a repository and every image in it. Pulls of its images fail at once; the storage is freed after the next nightly garbage collection. Needs an AI app connected with Manage and delete access.",
		Risk:        Destructive, Destructive: true, Idempotent: true,
		Routes: []string{"GET /registry/repositories/{repository_id}", "DELETE /registry/repositories/{repository_id}"},
	}, func(ctx context.Context, c *Call, in deleteRegistryRepositoryInput) (*Preview, error) {
		if err := checkIDs("repository_id", in.RepositoryID); err != nil {
			return nil, err
		}
		r, err := c.getRepository(ctx, in.RepositoryID)
		if err != nil {
			return nil, err
		}
		return &Preview{
			Action: fmt.Sprintf("Delete repository %s with its %s (%s). This cannot be undone.", r.ID, plural(r.TagCount, "tag", "tags"), gib(r.StorageBytes)),
			Warnings: []string{
				deleteAccessNote,
				"Anything that pulls these images (servers, clusters, CI) fails to pull them from now on.",
			},
			Data: map[string]any{"repository": r},
		}, nil
	}, func(ctx context.Context, c *Call, in deleteRegistryRepositoryInput) (*Result, error) {
		var r registryRepository
		if err := c.Client.Do(ctx, "DELETE", repositoryPath(in.RepositoryID), nil, nil, &r, c.writeOpts()...); err != nil {
			return nil, err
		}
		return ok(fmt.Sprintf("Repository %s is being deleted. Its storage is freed after the next nightly garbage collection.", in.RepositoryID), r)
	})

	add(Spec{
		Name: "list_registry_tags", Title: "List image tags",
		Description: "List a repository's tags, newest push first: each one's digest, size, platforms, push and last pull time. Paginated.",
		Risk:        Read, Idempotent: true,
		Routes: []string{"GET /registry/repositories/{repository_id}/tags"},
	}, func(ctx context.Context, c *Call, in listRegistryTagsInput) (*Result, error) {
		if err := checkIDs("repository_id", in.RepositoryID); err != nil {
			return nil, err
		}
		q := pageQuery(in.pageInput)
		setIf(q, "q", in.Q)
		var page pantech.Page[registryTag]
		if err := c.Client.Do(ctx, "GET", repositoryPath(in.RepositoryID)+"/tags", q, nil, &page); err != nil {
			return nil, err
		}
		return pageResult(&page, "tag", "tags")
	})

	add(Spec{
		Name: "get_registry_tag", Title: "Get an image tag",
		Description: "Get one tag of a repository: the image digest it points at, media type, size, platforms, when it was pushed and last pulled, and whether it is immutable.",
		Risk:        Read, Idempotent: true,
		Routes: []string{"GET /registry/repositories/{repository_id}/tags/{tag}"},
	}, func(ctx context.Context, c *Call, in registryTagInput) (*Result, error) {
		t, err := c.getTag(ctx, in.RepositoryID, in.Tag)
		if err != nil {
			return nil, err
		}
		return ok(fmt.Sprintf("Tag %s of repository %s: %s, pushed %s.", arg(t.Name), in.RepositoryID, gib(t.SizeBytes), arg(t.PushedAt)), t)
	})

	addConfirmed(Spec{
		Name: "delete_registry_tag", Title: "Delete an image tag",
		Description: "Delete a tag from a repository. Pulls of that tag fail at once; the image is removed and its storage freed after the next nightly garbage collection if no other tag points at it. Refused when the repository has immutable tags. Needs an AI app connected with Manage and delete access.",
		Risk:        Destructive, Destructive: true, Idempotent: true,
		Routes: []string{"GET /registry/repositories/{repository_id}", "GET /registry/repositories/{repository_id}/tags/{tag}", "DELETE /registry/repositories/{repository_id}/tags/{tag}"},
	}, func(ctx context.Context, c *Call, in deleteRegistryTagInput) (*Preview, error) {
		t, err := c.getTag(ctx, in.RepositoryID, in.Tag)
		if err != nil {
			return nil, err
		}
		r, err := c.getRepository(ctx, in.RepositoryID)
		if err != nil {
			return nil, err
		}
		p := &Preview{
			Action: fmt.Sprintf("Delete tag %s from repository %s. This cannot be undone.", in.Tag, r.ID),
			Warnings: []string{
				deleteAccessNote,
				"Anything that pulls this tag fails to pull it from now on.",
			},
			Data: map[string]any{"repository": r, "tag": t},
		}
		if r.ImmutableTags {
			p.Warnings = append(p.Warnings, "The repository has immutable tags, so Pantech refuses (TAG_IMMUTABLE). Turn immutable tags off first with update_registry_repository.")
		}
		return p, nil
	}, func(ctx context.Context, c *Call, in deleteRegistryTagInput) (*Result, error) {
		if err := c.Client.Do(ctx, "DELETE", repositoryPath(in.RepositoryID)+"/tags/"+in.Tag, nil, nil, nil, c.writeOpts()...); err != nil {
			return nil, err
		}
		return ok(fmt.Sprintf("Tag %s deleted from repository %s.", in.Tag, in.RepositoryID),
			map[string]any{"repository_id": in.RepositoryID, "tag": in.Tag, "status": "deleted"})
	})

	add(Spec{
		Name: "list_registry_tokens", Title: "List registry tokens",
		Description: "List the organization's registry tokens (credentials for CI and clusters that work only with the registry): permission, repositories, expiry and last use. Secrets are never returned.",
		Risk:        Read, Idempotent: true,
		Routes: []string{"GET /registry/tokens"},
	}, func(ctx context.Context, c *Call, in pageInput) (*Result, error) {
		var page pantech.Page[registryToken]
		if err := c.Client.Do(ctx, "GET", "/registry/tokens", pageQuery(in), nil, &page); err != nil {
			return nil, err
		}
		for i := range page.Data {
			page.Data[i].Secret = ""
		}
		return pageResult(&page, "registry token", "registry tokens")
	})

	addConfirmed(Spec{
		Name: "create_registry_token", Title: "Create a registry token",
		Description: "Create a registry token for CI or a cluster: pull (read images) or push (push and pull), for every repository or a list, with an optional expiry. It works only with the registry, never with the Pantech API. Its secret is shown once, in this tool's result: the user must store it then. Owners and admins only; at most 50 tokens.",
		Risk:        Exposure,
		Routes:      []string{"POST /registry/tokens"},
	}, func(ctx context.Context, c *Call, in createRegistryTokenInput) (*Preview, error) {
		if err := checkCreateToken(in); err != nil {
			return nil, err
		}
		scope := "every repository in the namespace"
		if len(in.Repositories) > 0 {
			scope = plural(len(in.Repositories), "repository", "repositories") + " (in the data)"
		}
		expiry := "It never expires: revoke it when it is no longer needed."
		if in.ExpiresAt != "" {
			expiry = "It expires " + in.ExpiresAt + "."
		}
		p := &Preview{
			Action:   fmt.Sprintf("Create a %s registry token for %s. %s", in.Permission, scope, expiry),
			Warnings: []string{"The secret is shown once, in the result: the user must store it then (for example as a CI secret). Owners and admins are emailed a security notice."},
			Data:     map[string]any{"request": in},
		}
		if in.Permission == "push" {
			p.Warnings = append(p.Warnings, "A push token can also overwrite tags that are not immutable.")
		}
		return p, nil
	}, func(ctx context.Context, c *Call, in createRegistryTokenInput) (*Result, error) {
		if err := checkCreateToken(in); err != nil {
			return nil, err
		}
		body := map[string]any{"name": in.Name, "permission": in.Permission}
		if len(in.Repositories) > 0 {
			body["repositories"] = in.Repositories
		}
		if in.ExpiresAt != "" {
			body["expires_at"] = in.ExpiresAt
		}
		var t registryToken
		if err := c.Client.Do(ctx, "POST", "/registry/tokens", nil, body, &t, c.writeOpts()...); err != nil {
			return nil, err
		}
		if t.Secret == "" {
			return ok(fmt.Sprintf("Registry token %s exists, but its secret is not shown again (it was returned once, when it was created). If it was not stored, revoke it and create a new one.", arg(t.ID)), t)
		}
		return ok(fmt.Sprintf("Created registry token %s. Its secret is in the data and is never shown again: give it to the user and tell them to store it now (for example as a CI secret). Log in with: docker login %s -u pantech, with the secret as the password.", arg(t.ID), registryHost), t)
	})

	addConfirmed(Spec{
		Name: "revoke_registry_token", Title: "Revoke a registry token",
		Description: "Revoke a registry token. New logins with it fail at once; logins already made stop working within 5 minutes. Needs an AI app connected with Manage and delete access.",
		Risk:        Destructive, Destructive: true, Idempotent: true,
		Routes: []string{"GET /registry/tokens", "DELETE /registry/tokens/{token_id}"},
	}, func(ctx context.Context, c *Call, in revokeRegistryTokenInput) (*Preview, error) {
		if err := checkIDs("token_id", in.TokenID); err != nil {
			return nil, err
		}
		p := &Preview{
			Action: fmt.Sprintf("Revoke registry token %s. Whatever uses it (CI, a cluster) can no longer push or pull within 5 minutes.", in.TokenID),
			Warnings: []string{
				deleteAccessNote,
			},
			Data: map[string]any{"token_id": in.TokenID},
		}
		if t, err := c.findToken(ctx, in.TokenID); err != nil {
			return nil, err
		} else if t != nil {
			p.Data = map[string]any{"token": t}
		}
		return p, nil
	}, func(ctx context.Context, c *Call, in revokeRegistryTokenInput) (*Result, error) {
		if err := c.Client.Do(ctx, "DELETE", "/registry/tokens/"+in.TokenID, nil, nil, nil, c.writeOpts()...); err != nil {
			return nil, err
		}
		return ok(fmt.Sprintf("Registry token %s is revoked.", in.TokenID), map[string]any{"token_id": in.TokenID, "status": "revoked"})
	})
}

func checkCreateRepository(in createRegistryRepositoryInput) error {
	if err := checkRepositoryName(in.Name); err != nil {
		return err
	}
	if err := checkVisibility(in.Visibility); err != nil {
		return err
	}
	return in.Retention.check()
}

func checkCreateToken(in createRegistryTokenInput) error {
	if strings.TrimSpace(in.Name) == "" {
		return errorf("name is required, e.g. github-actions.")
	}
	if in.Permission != "pull" && in.Permission != "push" {
		return errorf("permission must be pull or push.")
	}
	for i, r := range in.Repositories {
		if checkRepositoryName(r) != nil {
			return errorf("repositories[%d] must be a repository name in the namespace, e.g. api/server.", i)
		}
	}
	if in.ExpiresAt != "" {
		if _, err := time.Parse(time.RFC3339, in.ExpiresAt); err != nil {
			return errorf("expires_at must be an RFC 3339 time, e.g. 2027-11-10T00:00:00Z.")
		}
	}
	return nil
}

// repositoryChangeNeedsConfirmation reports whether a change opens the
// repository, lets its tags move, or sets rules that delete images.
func repositoryChangeNeedsConfirmation(in updateRegistryRepositoryInput) bool {
	return in.Visibility == "public" || isFalse(in.ImmutableTags) || in.Retention != nil
}

func repositoryChange(in updateRegistryRepositoryInput) (map[string]any, error) {
	if err := checkIDs("repository_id", in.RepositoryID); err != nil {
		return nil, err
	}
	if err := checkVisibility(in.Visibility); err != nil {
		return nil, err
	}
	if err := in.Retention.check(); err != nil {
		return nil, err
	}
	body := map[string]any{}
	if in.Description != nil {
		body["description"] = *in.Description
	}
	if in.Visibility != "" {
		body["visibility"] = in.Visibility
	}
	if in.ImmutableTags != nil {
		body["immutable_tags"] = *in.ImmutableTags
	}
	if in.Retention != nil {
		body["retention"] = in.Retention
	}
	if len(body) == 0 {
		return nil, errorf("Nothing to change: give description, visibility, immutable_tags or retention.")
	}
	return body, nil
}

func (c *Call) getRepository(ctx context.Context, id string) (*registryRepository, error) {
	var r registryRepository
	if err := c.Client.Do(ctx, "GET", repositoryPath(id), nil, nil, &r); err != nil {
		return nil, err
	}
	return &r, nil
}

func (c *Call) getTag(ctx context.Context, repositoryID, tag string) (*registryTag, error) {
	if err := checkIDs("repository_id", repositoryID); err != nil {
		return nil, err
	}
	if err := checkTag(tag); err != nil {
		return nil, err
	}
	var t registryTag
	if err := c.Client.Do(ctx, "GET", repositoryPath(repositoryID)+"/tags/"+tag, nil, nil, &t); err != nil {
		return nil, err
	}
	return &t, nil
}

// findToken looks a token up in the list (the API has no single-token
// read), following pages; nil when it is not there.
func (c *Call) findToken(ctx context.Context, id string) (*registryToken, error) {
	cursor := ""
	for range 20 {
		q := url.Values{"limit": {"100"}}
		setIf(q, "cursor", cursor)
		var page pantech.Page[registryToken]
		if err := c.Client.Do(ctx, "GET", "/registry/tokens", q, nil, &page); err != nil {
			return nil, err
		}
		for i := range page.Data {
			if page.Data[i].ID == id {
				t := page.Data[i]
				t.Secret = ""
				return &t, nil
			}
		}
		if page.NextCursor == nil || *page.NextCursor == "" || *page.NextCursor == cursor {
			return nil, nil
		}
		cursor = *page.NextCursor
	}
	return nil, nil
}
