package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
)

// Moving what a migration plan lists: buckets (copied by Pantech through a
// separate copy role the user grants for chosen buckets), servers (their
// disks copied into a new instance, through the agent or AWS snapshots),
// databases (through the agent in the user's network, whose source
// credentials never reach Pantech) and Kubernetes clusters (exported by the
// agent and applied to a Pantech cluster). Each move is free; what it creates
// on Pantech bills as usual. The copy role's and server copy role's templates
// stay out of results like the connection's, and a Kubernetes move's
// transformed manifests are never fetched (excluded.go): only names and
// counts are shown. Anything taken from the source (bucket names, object
// keys, table names, Kubernetes object names) is the customer's own text and
// goes only in the fenced data.

// moveState is what a summary reads from any move's answer.
type moveState struct {
	ID      string        `json:"id"`
	Status  string        `json:"status"`
	Failure *cloudFailure `json:"failure"`
}

// stateText is "<kind> <id> is <status>", with the failure code when it failed.
func stateText(kind string, st *moveState) string {
	id := st.ID
	if checkIDs("id", id) != nil {
		id = "(see the data)"
	}
	text := fmt.Sprintf("%s %s is %s.", kind, id, arg(st.Status))
	if st.Failure != nil {
		text += fmt.Sprintf(" It failed (%s): the failure's message in the data says what to do.", arg(failureCode(st.Failure)))
	}
	return text
}

// countList reads a list answer: its length for the summary, the items for
// the data.
func (c *Call) countList(ctx context.Context, path, one, many, next string) (*Result, error) {
	var page struct {
		Data []json.RawMessage `json:"data"`
	}
	if err := c.Client.Do(ctx, "GET", path, nil, nil, &page); err != nil {
		return nil, err
	}
	return ok(plural(len(page.Data), one, many)+", newest first. "+next, page.Data)
}

// copyAccess is a connection's copy access without the copy role's template.
type copyAccess struct {
	ConnectionID string        `json:"connection_id"`
	Status       string        `json:"status"`
	Failure      *cloudFailure `json:"failure"`
	RoleARN      *string       `json:"role_arn"`
	Buckets      []struct {
		Name       string        `json:"name"`
		Region     *string       `json:"region"`
		Versioning *string       `json:"versioning"`
		Ready      bool          `json:"ready"`
		Failure    *cloudFailure `json:"failure"`
	} `json:"buckets"`
	VerifiedAt *string `json:"verified_at"`
	Setup      struct {
		Subject string `json:"subject"`
		// TemplateReady says the copy role's template can be fetched; it is
		// not shown here.
		TemplateReady bool `json:"template_ready"`
	} `json:"setup"`
}

func (a *copyAccess) UnmarshalJSON(b []byte) error {
	type plain copyAccess
	var raw struct {
		plain
		Setup struct {
			Subject  string  `json:"subject"`
			Template *string `json:"template"`
		} `json:"setup"`
	}
	if err := json.Unmarshal(b, &raw); err != nil {
		return err
	}
	*a = copyAccess(raw.plain)
	a.Setup.Subject = raw.Setup.Subject
	a.Setup.TemplateReady = raw.Setup.Template != nil && *raw.Setup.Template != ""
	return nil
}

func copyAccessText(a *copyAccess) string {
	ready := 0
	for _, b := range a.Buckets {
		if b.Ready {
			ready++
		}
	}
	head := fmt.Sprintf("Copy access for connection %s is %s: %s chosen, %d verified.", arg(a.ConnectionID), arg(a.Status), plural(len(a.Buckets), "bucket", "buckets"), ready)
	switch a.Status {
	case "not_requested":
		return head + " Choose the buckets with set_bucket_copy_access."
	case "awaiting_role":
		return head + " The user creates a stack from the copy role's template (in AWS, next to the connection's stack; `pantech migrate buckets access` in the CLI saves the template, and the API returns it as setup.template in GET /public/v1/cloud-connections/{connection_id}/copy-access), then sends its CopyRoleArn output with set_bucket_copy_access."
	case "verifying":
		return head + " Pantech is checking the role against each bucket without reading any object; call get_bucket_copy_access again shortly."
	case "verified":
		return head + " Next: start_bucket_import for a verified bucket."
	case "failed":
		return head + " The failure's message in the data says what to do; then set_bucket_copy_access with retry."
	}
	return head
}

type copyAccessInput struct {
	ConnectionID string   `json:"connection_id" jsonschema:"An AWS connection's id, e.g. ccn_…"`
	Buckets      []string `json:"buckets,omitempty" jsonschema:"The source buckets Pantech may copy, by name, 1 to 50, each once; required the first time, and it replaces the list."`
	RoleARN      string   `json:"role_arn,omitempty" jsonschema:"The copy stack's CopyRoleArn output, like arn:aws:iam::123456789012:role/name. Never access keys."`
	Retry        bool     `json:"retry,omitempty" jsonschema:"Verify the copy role again."`
}

type startBucketImportInput struct {
	ConnectionID          string `json:"connection_id" jsonschema:"The connection whose copy access covers the bucket, e.g. ccn_…"`
	SourceBucket          string `json:"source_bucket" jsonschema:"The source bucket's name; it must be verified in get_bucket_copy_access."`
	DestinationBucketName string `json:"destination_bucket_name,omitempty" jsonschema:"A new Pantech bucket to create and copy into (billed as any bucket). Give this or destination_bucket_id."`
	DestinationBucketID   string `json:"destination_bucket_id,omitempty" jsonschema:"An existing Pantech bucket to copy into, e.g. bkt_…"`
	Versioning            bool   `json:"versioning,omitempty" jsonschema:"Turn versioning on in a new bucket."`
	PublicRead            bool   `json:"public_read,omitempty" jsonschema:"Make a new bucket readable by anyone on the internet. Off unless the user asks, even when the source was public."`
	MigrationID           string `json:"migration_id,omitempty" jsonschema:"The plan the move belongs to, e.g. mig_…"`
	Confirmation
}

type bucketImportInput struct {
	ImportID string `json:"import_id" jsonschema:"The bucket import's id, from start_bucket_import or list_bucket_imports."`
}

type resyncBucketImportInput struct {
	ImportID      string `json:"import_id" jsonschema:"The bucket import's id."`
	MirrorDeletes bool   `json:"mirror_deletes,omitempty" jsonschema:"Also delete, in the Pantech bucket, the objects this import copied that the source no longer has (never anything else). Only when the user asks for it."`
	Confirmation
}

type startServerCopyInput struct {
	MigrationID     string `json:"migration_id" jsonschema:"The migration plan's id, e.g. mig_…"`
	ItemKey         string `json:"item_key" jsonschema:"The planned server's item key, exactly as get_migration returned it."`
	Source          string `json:"source,omitempty" jsonschema:"agent (the default: the Pantech Migrate agent on the server sends its disks) or aws_snapshot (Pantech reads EBS snapshots through a separate copy role)."`
	Name            string `json:"name,omitempty" jsonschema:"The new instance's name; the source's when left out."`
	PlanSlug        string `json:"plan_slug,omitempty" jsonschema:"Another Pantech plan than the item's (list_plans)."`
	SubnetID        string `json:"subnet_id,omitempty" jsonschema:"The subnet for the new instance, e.g. sub_…"`
	SecurityGroupID string `json:"security_group_id,omitempty" jsonschema:"The security group for the new instance, e.g. sg_…"`
	SSHKeyID        string `json:"ssh_key_id,omitempty" jsonschema:"Optional: an SSH key to add; the copied disk keeps its own users and keys."`
	CopyRoleARN     string `json:"copy_role_arn,omitempty" jsonschema:"For aws_snapshot: the copy stack's CopyRoleArn output (pantech migrate servers template in the CLI saves the stack's template)."`
	CreateSnapshots bool   `json:"create_snapshots,omitempty" jsonschema:"For aws_snapshot: the copy role snapshots the server's volumes itself."`
}

type serverCopyInput struct {
	CopyID string `json:"copy_id" jsonschema:"The server copy's id, e.g. sco_…"`
}

type cutOverServerCopyInput struct {
	CopyID string `json:"copy_id" jsonschema:"The server copy's id, e.g. sco_…"`
	Confirmation
}

type startDatabaseImportInput struct {
	MigrationID           string `json:"migration_id" jsonschema:"The migration plan's id, e.g. mig_…"`
	ItemKey               string `json:"item_key" jsonschema:"One of the plan's database items, by key exactly as get_migration returned it."`
	DestinationDatabaseID string `json:"destination_database_id" jsonschema:"One of the user's Pantech databases with the same engine, e.g. db_… (create it first)."`
	SourceRef             string `json:"source_ref" jsonschema:"The source's name in the user's Migrate agent configuration (letters, digits, '.', '_' and '-'); its address and login stay on the agent."`
	TargetDatabase        string `json:"target_database,omitempty" jsonschema:"The database to create and fill on the destination; it must be empty. Default: the source's name."`
	Mode                  string `json:"mode,omitempty" jsonschema:"dump (copies once: stop writes to the source first) or replicate (PostgreSQL: keeps following changes until the cut-over)."`
}

type databaseImportInput struct {
	ImportID string `json:"import_id" jsonschema:"The database import's id."`
}

type startKubernetesMoveInput struct {
	MigrationID          string   `json:"migration_id" jsonschema:"The migration plan's id, e.g. mig_…"`
	ItemKey              string   `json:"item_key" jsonschema:"One of the plan's kubernetes_cluster items, by key exactly as get_migration returned it."`
	ClusterID            string   `json:"cluster_id" jsonschema:"A running Pantech cluster of the organization, e.g. k8s_…"`
	IncludeSecrets       bool     `json:"include_secrets,omitempty" jsonschema:"Also move the source's Secrets (cluster to cluster; their values never pass through a tool). Only when the user asks."`
	CopyVolumes          bool     `json:"copy_volumes,omitempty" jsonschema:"Also copy the data of the source's persistent volumes."`
	PublishLoadBalancers *bool    `json:"publish_load_balancers,omitempty" jsonschema:"Create Pantech load balancers for the source's LoadBalancer services (billed, and reachable from the internet). Default true."`
	Namespaces           []string `json:"namespaces,omitempty" jsonschema:"Only these namespaces; every namespace when left out."`
}

type kubernetesMoveInput struct {
	MoveID string `json:"move_id" jsonschema:"The Kubernetes move's id, e.g. kmv_…"`
}

type kubernetesRef struct {
	Group     string `json:"group" jsonschema:"The API group; empty for the core API."`
	Kind      string `json:"kind"`
	Namespace string `json:"namespace"`
	Name      string `json:"name"`
}

type previewKubernetesMoveInput struct {
	MoveID          string          `json:"move_id" jsonschema:"The Kubernetes move's id, e.g. kmv_…"`
	AllowPrivileged []kubernetesRef `json:"allow_privileged,omitempty" jsonschema:"Workloads the cluster policy refused (policy_refused in get_kubernetes_move) that the user chose to include anyway. Only from the user's own words."`
}

type applyKubernetesMoveInput struct {
	MoveID string `json:"move_id" jsonschema:"The Kubernetes move's id, e.g. kmv_…"`
	Confirmation
}

type cutOverKubernetesMoveInput struct {
	MoveID              string `json:"move_id" jsonschema:"The Kubernetes move's id, e.g. kmv_…"`
	SourceWritesStopped bool   `json:"source_writes_stopped" jsonschema:"true only once the user said writes on the source cluster have stopped."`
}

var sourceRefPattern = regexp.MustCompile(`^[A-Za-z0-9._-]{1,64}$`)

// writeMove sends one write of a move and summarises its answer.
func (c *Call) writeMove(ctx context.Context, method, path string, body any, kind, next string) (*Result, error) {
	raw, st, err := c.rawResult(ctx, method, path, body, true)
	if err != nil {
		return nil, err
	}
	return ok(stateText(kind, st)+" "+next, raw)
}

// readMove gets one move and summarises it.
func (c *Call) readMove(ctx context.Context, path, kind string, next func(*moveState) string) (*Result, error) {
	raw, st, err := c.rawResult(ctx, "GET", path, nil, false)
	if err != nil {
		return nil, err
	}
	return ok(stateText(kind, st)+" "+next(st), raw)
}

func bucketImportNext(st *moveState) string {
	switch st.Status {
	case "queued", "running":
		return "Progress is in the data; call get_bucket_import again in a while."
	case "succeeded":
		return "Objects that failed, if any, are listed in the data. After writes to the source stop, run resync_bucket_import once more, then switch the application over."
	}
	return ""
}

func serverCopyNext(st *moveState) string {
	switch st.Status {
	case "waiting", "copying", "importing":
		return "Progress is in the data; call get_server_copy again in a while."
	case "synced":
		return "sync_server_copy copies what changed since; when the user has stopped the application, cut_over_server_copy builds the instance."
	case "creating_instance":
		return "The instance is being created and billed like any new instance."
	case "completed":
		return "The instance id is in the data (instance.id)."
	}
	return ""
}

func databaseImportNext(st *moveState) string {
	switch st.Status {
	case "awaiting_agent":
		return "The user's Pantech Migrate agent must be running, enrolled for the plan's connection, with this source_ref under database_sources in its configuration; pantech-migrate-agent check tests each source without sending anything."
	case "blocked":
		return "Read each blocking issue's message and fix in the data to the user; after they fix the source, check_database_import_readiness."
	case "ready", "preflight", "copying", "cutting_over", "verifying":
		return "Progress is in the data; call get_database_import again in a while."
	case "replicating":
		return "It follows the source's changes (lag_bytes in the data). When the user has stopped writes to the source, cut_over_database_import."
	case "completed":
		return "Every table was verified. revoke_database_import_access removes the import's login if it is still there."
	}
	return ""
}

func kubernetesMoveNext(st *moveState) string {
	switch st.Status {
	case "exporting", "previewing", "applying", "syncing", "cutting_over":
		return "Call get_kubernetes_move again in a while."
	case "ready":
		return "Read the report in the data with the user (refused and flagged objects, volumes, images), then preview_kubernetes_move."
	case "applied":
		return "Workloads that mount a copied volume wait scaled down: sync_kubernetes_move_volumes copies the data again; when writes on the source have stopped, cut_over_kubernetes_move."
	}
	return ""
}

func init() {
	// --- buckets ---

	add(Spec{
		Name: "get_bucket_copy_access", Title: "Get a connection's bucket copy access",
		Description: "Get an AWS connection's opt-in copy access: the buckets chosen for copying, whether each is verified, and the copy role (separate from the connection's read-only role, and able to read only the chosen buckets' objects). The role's template is not shown.",
		Risk:        Read, Idempotent: true,
		Routes: []string{"GET /cloud-connections/{connection_id}/copy-access"},
	}, func(ctx context.Context, c *Call, in cloudConnectionInput) (*Result, error) {
		if err := checkIDs("connection_id", in.ConnectionID); err != nil {
			return nil, err
		}
		var a copyAccess
		if err := c.Client.Do(ctx, "GET", "/cloud-connections/"+in.ConnectionID+"/copy-access", nil, nil, &a); err != nil {
			return nil, err
		}
		return ok(copyAccessText(&a), a)
	})

	add(Spec{
		Name: "set_bucket_copy_access", Title: "Choose the buckets Pantech may copy",
		Description: "Choose which source buckets Pantech may copy (buckets, which replaces the list), send the copy stack's CopyRoleArn output (role_arn), or verify the role again (retry). The copy role can read only the chosen buckets; Pantech verifies it against each bucket without reading any object. Nothing is copied or billed until start_bucket_import. Owners and admins only. " + migrateGuide,
		Risk:        Write, Idempotent: true,
		Routes: []string{"PUT /cloud-connections/{connection_id}/copy-access"},
	}, func(ctx context.Context, c *Call, in copyAccessInput) (*Result, error) {
		if err := checkIDs("connection_id", in.ConnectionID); err != nil {
			return nil, err
		}
		body := map[string]any{}
		if len(in.Buckets) > 0 {
			if len(in.Buckets) > 50 {
				return nil, errorf("buckets takes at most 50 buckets.")
			}
			body["buckets"] = in.Buckets
		}
		if in.RoleARN != "" {
			if !roleARNPattern.MatchString(in.RoleARN) {
				return nil, errorf("role_arn must be the copy stack's CopyRoleArn output, like arn:aws:iam::123456789012:role/name.")
			}
			body["role_arn"] = in.RoleARN
		}
		if in.Retry {
			body["retry"] = true
		}
		if len(body) == 0 {
			return nil, errorf("Nothing to change: give buckets, role_arn or retry.")
		}
		var a copyAccess
		if err := c.Client.Do(ctx, "PUT", "/cloud-connections/"+in.ConnectionID+"/copy-access", nil, body, &a, c.writeOpts()...); err != nil {
			return nil, err
		}
		return ok(copyAccessText(&a), a)
	})

	add(Spec{
		Name: "list_bucket_imports", Title: "List bucket imports",
		Description: "List the organization's bucket imports (source buckets copied into Pantech buckets), newest first, with status and progress.",
		Risk:        Read, Idempotent: true,
		Routes: []string{"GET /bucket-imports"},
	}, func(ctx context.Context, c *Call, _ noInput) (*Result, error) {
		return c.countList(ctx, "/bucket-imports", "bucket import", "bucket imports", "get_bucket_import shows one with its failed objects and audit trail.")
	})

	add(Spec{
		Name: "get_bucket_import", Title: "Get a bucket import",
		Description: "Get a bucket import: its status (queued, running, succeeded, failed or cancelled), the current pass and its progress, the first 100 objects that failed it and why, and its audit trail.",
		Risk:        Read, Idempotent: true,
		Routes: []string{"GET /bucket-imports/{import_id}"},
	}, func(ctx context.Context, c *Call, in bucketImportInput) (*Result, error) {
		if err := checkIDs("import_id", in.ImportID); err != nil {
			return nil, err
		}
		return c.readMove(ctx, "/bucket-imports/"+in.ImportID, "Bucket import", bucketImportNext)
	})

	addConfirmed(Spec{
		Name: "start_bucket_import", Title: "Copy a bucket into Pantech",
		Description: "Copy one source bucket's objects into a Pantech bucket: a new one (destination_bucket_name, created now) or one the organization has (destination_bucket_id). The bucket must be verified in get_bucket_copy_access. Objects are checked against their MD5s and keep their content type, cache control, content disposition, metadata and tags; only current versions are copied and nothing is deleted on either side. The move is free; the Pantech bucket's storage is billed per GiB-month as usual. Owners and admins only.",
		Risk:        Cost,
		Routes:      []string{"GET /cloud-connections/{connection_id}/copy-access", "POST /bucket-imports"},
	}, func(ctx context.Context, c *Call, in startBucketImportInput) (*Preview, error) {
		if err := checkIDs("connection_id", in.ConnectionID); err != nil {
			return nil, err
		}
		if err := checkOptionalIDs("destination_bucket_id", in.DestinationBucketID, "migration_id", in.MigrationID); err != nil {
			return nil, err
		}
		if in.SourceBucket == "" || (in.DestinationBucketName == "") == (in.DestinationBucketID == "") {
			return nil, errorf("Give source_bucket, and exactly one of destination_bucket_name (a new bucket) and destination_bucket_id.")
		}
		var a copyAccess
		if err := c.Client.Do(ctx, "GET", "/cloud-connections/"+in.ConnectionID+"/copy-access", nil, nil, &a); err != nil {
			return nil, err
		}
		p := &Preview{
			Action: fmt.Sprintf("Copy a source bucket of connection %s into a Pantech bucket (the request is in the data). Nothing is deleted on either side.", in.ConnectionID),
			Price:  "The move is free; the Pantech bucket's storage is billed per GiB-month, as any bucket",
			Data:   map[string]any{"request": in, "copy_access": a},
		}
		ready := false
		for _, b := range a.Buckets {
			if b.Name == in.SourceBucket && b.Ready {
				ready = true
			}
		}
		if !ready {
			p.Warnings = append(p.Warnings, "That bucket is not verified in this connection's copy access: Pantech refuses (COPY_ACCESS_NOT_READY). Use set_bucket_copy_access first.")
		}
		if in.PublicRead {
			p.Warnings = append(p.Warnings, "public_read makes the new bucket readable by anyone on the internet.")
		}
		return p, nil
	}, func(ctx context.Context, c *Call, in startBucketImportInput) (*Result, error) {
		body := map[string]any{"connection_id": in.ConnectionID, "source_bucket": in.SourceBucket}
		for k, v := range map[string]string{"destination_bucket_name": in.DestinationBucketName, "destination_bucket_id": in.DestinationBucketID, "migration_id": in.MigrationID} {
			if v != "" {
				body[k] = v
			}
		}
		if in.Versioning {
			body["versioning"] = true
		}
		if in.PublicRead {
			body["public_read"] = true
		}
		return c.writeMove(ctx, "POST", "/bucket-imports", body, "Bucket import", "Follow it with get_bucket_import.")
	})

	addConfirmedWith(Spec{
		Name: "resync_bucket_import", Title: "Copy a bucket's changes again",
		Description: "Queue another pass of a finished bucket import: objects already copied unchanged are skipped, so it copies only what changed. Run a last one after writes to the source stop, then switch over. mirror_deletes also deletes, in the Pantech bucket, the objects this import copied that the source no longer has (never anything else); it needs an AI app connected with Manage and delete access, and is recorded in the import's audit trail.",
		Risk:        Destructive, Destructive: true,
		ConfirmWhen: "when mirror_deletes is true",
		Routes:      []string{"GET /bucket-imports/{import_id}", "POST /bucket-imports/{import_id}/resync"},
	}, confirmOptions[resyncBucketImportInput]{when: func(in resyncBucketImportInput) bool { return in.MirrorDeletes }},
		func(ctx context.Context, c *Call, in resyncBucketImportInput) (*Preview, error) {
			if err := checkIDs("import_id", in.ImportID); err != nil {
				return nil, err
			}
			raw, _, err := c.rawResult(ctx, "GET", "/bucket-imports/"+in.ImportID, nil, false)
			if err != nil {
				return nil, err
			}
			return &Preview{
				Action:   fmt.Sprintf("Copy what changed into bucket import %s's Pantech bucket, and delete there the objects this import copied that the source no longer has. Deleted objects cannot be recovered.", in.ImportID),
				Warnings: []string{deleteAccessNote},
				Data:     map[string]any{"bucket_import": raw},
			}, nil
		}, func(ctx context.Context, c *Call, in resyncBucketImportInput) (*Result, error) {
			if err := checkIDs("import_id", in.ImportID); err != nil {
				return nil, err
			}
			body := map[string]any{}
			if in.MirrorDeletes {
				body["mirror_deletes"] = true
			}
			return c.writeMove(ctx, "POST", "/bucket-imports/"+in.ImportID+"/resync", body, "Bucket import", "Follow it with get_bucket_import.")
		})

	add(Spec{
		Name: "cancel_bucket_import", Title: "Cancel a bucket import",
		Description: "Stop a bucket import: a queued one at once, a running one at its next checkpoint. Objects already copied stay in the Pantech bucket. Owners and admins only.",
		Risk:        Write, Idempotent: true,
		Routes: []string{"POST /bucket-imports/{import_id}/cancel"},
	}, func(ctx context.Context, c *Call, in bucketImportInput) (*Result, error) {
		if err := checkIDs("import_id", in.ImportID); err != nil {
			return nil, err
		}
		return c.writeMove(ctx, "POST", "/bucket-imports/"+in.ImportID+"/cancel", nil, "Bucket import", "Objects already copied stay.")
	})

	// --- servers ---

	add(Spec{
		Name: "list_server_copies", Title: "List server copies",
		Description: "List the organization's server copies, newest first, with each one's status and progress.",
		Risk:        Read, Idempotent: true,
		Routes: []string{"GET /server-copies"},
	}, func(ctx context.Context, c *Call, _ noInput) (*Result, error) {
		return c.countList(ctx, "/server-copies", "server copy", "server copies", "get_server_copy shows one.")
	})

	add(Spec{
		Name: "get_server_copy", Title: "Get a server copy",
		Description: "Get a server copy: its status (waiting, copying, synced, importing, creating_instance, completed, failed or cancelled), the pre-flight's findings, the running pass and its progress (bytes and an estimate of the time left), and the instance once it is being created.",
		Risk:        Read, Idempotent: true,
		Routes: []string{"GET /server-copies/{copy_id}"},
	}, func(ctx context.Context, c *Call, in serverCopyInput) (*Result, error) {
		if err := checkIDs("copy_id", in.CopyID); err != nil {
			return nil, err
		}
		return c.readMove(ctx, "/server-copies/"+in.CopyID, "Server copy", serverCopyNext)
	})

	add(Spec{
		Name: "start_server_copy", Title: "Start copying a server",
		Description: "Start copying one planned server's disks into a new Pantech instance, with the item's plan (or plan_slug), the subnet and security group given, and optionally an SSH key. Nothing is created yet and the copy is free: the first pass copies every disk, sync_server_copy copies what changed since, and cut_over_server_copy builds the billed instance. With source agent (the default), on any cloud, the user enrolls the Pantech Migrate agent on the server itself for the plan's connection and runs it there as root (Administrator on Windows) with run --server-copy and this copy's id: only an agent started that way is handed the copy, and it reads only that server's own disks (the boot disk unless --disk names more). A connection has one agent, so this replaces its long-running agent until that one is enrolled again. With aws_snapshot Pantech reads EBS snapshots through a separate copy role (copy_role_arn). Windows servers are copied with the agent only, and need Windows Server enabled for the organization. At most five copies in progress. Owners and admins only.",
		Risk:        Write,
		Routes:      []string{"POST /migrations/{migration_id}/server-copies"},
	}, func(ctx context.Context, c *Call, in startServerCopyInput) (*Result, error) {
		if err := checkIDs("migration_id", in.MigrationID); err != nil {
			return nil, err
		}
		if err := checkOptionalIDs("subnet_id", in.SubnetID, "security_group_id", in.SecurityGroupID, "ssh_key_id", in.SSHKeyID); err != nil {
			return nil, err
		}
		if in.ItemKey == "" {
			return nil, errorf("item_key is required: the planned server's key exactly as get_migration returned it.")
		}
		source := in.Source
		if source == "" {
			source = "agent"
		}
		body := map[string]any{"item_key": in.ItemKey, "source": source}
		switch source {
		case "agent":
			if in.CopyRoleARN != "" || in.CreateSnapshots {
				return nil, errorf("copy_role_arn and create_snapshots are for source aws_snapshot only.")
			}
		case "aws_snapshot":
			if !roleARNPattern.MatchString(in.CopyRoleARN) {
				return nil, errorf("source aws_snapshot needs copy_role_arn: the copy stack's CopyRoleArn output, like arn:aws:iam::123456789012:role/name.")
			}
			aws := map[string]any{"copy_role_arn": in.CopyRoleARN}
			if in.CreateSnapshots {
				aws["create_snapshots"] = true
			}
			body["aws"] = aws
		default:
			return nil, errorf("source must be agent or aws_snapshot.")
		}
		for k, v := range map[string]string{"name": in.Name, "plan_slug": in.PlanSlug, "subnet_id": in.SubnetID, "security_group_id": in.SecurityGroupID, "ssh_key_id": in.SSHKeyID} {
			if v != "" {
				body[k] = v
			}
		}
		return c.writeMove(ctx, "POST", "/migrations/"+in.MigrationID+"/server-copies", body, "Server copy", "With source agent, the user now runs pantech-migrate-agent run --server-copy <this copy's id> as root on the server, enrolled for the plan's connection; its first pass starts then (with aws_snapshot, once the copy role is ready). Follow it with get_server_copy.")
	})

	add(Spec{
		Name: "sync_server_copy", Title: "Copy a server's changes again",
		Description: "Ask a synced server copy for another pass that copies only what changed since the last one. Free. Owners and admins only.",
		Risk:        Write,
		Routes:      []string{"POST /server-copies/{copy_id}/sync"},
	}, func(ctx context.Context, c *Call, in serverCopyInput) (*Result, error) {
		if err := checkIDs("copy_id", in.CopyID); err != nil {
			return nil, err
		}
		return c.writeMove(ctx, "POST", "/server-copies/"+in.CopyID+"/sync", map[string]any{}, "Server copy", "Follow it with get_server_copy.")
	})

	addConfirmed(Spec{
		Name: "cut_over_server_copy", Title: "Cut over a server copy",
		Description: "Run a synced server copy's last pass and build its Pantech instance, through the normal create path: the instance is billed, paid for and limited like any new instance. The user stops the application on the source server first; the agent flushes and freezes what it can, then copies the last changes. Owners and admins only.",
		Risk:        Cost,
		Routes:      []string{"GET /server-copies/{copy_id}", "POST /server-copies/{copy_id}/cutover"},
	}, func(ctx context.Context, c *Call, in cutOverServerCopyInput) (*Preview, error) {
		if err := checkIDs("copy_id", in.CopyID); err != nil {
			return nil, err
		}
		var cp struct {
			moveState
			Instance struct {
				PlanSlug string `json:"plan_slug"`
				Region   string `json:"region"`
			} `json:"instance"`
		}
		raw, _, err := c.rawResult(ctx, "GET", "/server-copies/"+in.CopyID, nil, false)
		if err != nil {
			return nil, err
		}
		_ = json.Unmarshal(raw, &cp)
		p := &Preview{
			Action: fmt.Sprintf("Run server copy %s's last pass and create its Pantech instance on plan %s in %s.", in.CopyID, arg(cp.Instance.PlanSlug), arg(cp.Instance.Region)),
			Price:  fmt.Sprintf("The new instance is billed like any instance on plan %s (list_plans or estimate_price gives its price); the copy itself is free", arg(cp.Instance.PlanSlug)),
			Warnings: []string{
				"Stop the application on the source server first, or changes made after the last pass are lost on Pantech.",
			},
			Data: map[string]any{"server_copy": raw},
		}
		if cp.Status != "synced" {
			p.Warnings = append(p.Warnings, "The copy is not synced: Pantech refuses until its first pass has finished.")
		}
		return p, nil
	}, func(ctx context.Context, c *Call, in cutOverServerCopyInput) (*Result, error) {
		return c.writeMove(ctx, "POST", "/server-copies/"+in.CopyID+"/cutover", map[string]any{}, "Server copy", "Follow it with get_server_copy until it is completed.")
	})

	add(Spec{
		Name: "cancel_server_copy", Title: "Cancel a server copy",
		Description: "Stop a server copy and delete everything Pantech staged for it (the source server is not touched; a new copy starts from scratch). Refused once the instance is being created: it is the user's then, deleted like any instance. Owners and admins only.",
		Risk:        Write, Idempotent: true,
		Routes: []string{"POST /server-copies/{copy_id}/cancel"},
	}, func(ctx context.Context, c *Call, in serverCopyInput) (*Result, error) {
		if err := checkIDs("copy_id", in.CopyID); err != nil {
			return nil, err
		}
		return c.writeMove(ctx, "POST", "/server-copies/"+in.CopyID+"/cancel", map[string]any{}, "Server copy", "")
	})

	// --- databases ---

	add(Spec{
		Name: "list_database_imports", Title: "List database imports",
		Description: "List the organization's database imports, newest first, without their tables.",
		Risk:        Read, Idempotent: true,
		Routes: []string{"GET /database-imports"},
	}, func(ctx context.Context, c *Call, _ noInput) (*Result, error) {
		return c.countList(ctx, "/database-imports", "database import", "database imports", "get_database_import shows one with its readiness and tables.")
	})

	add(Spec{
		Name: "get_database_import", Title: "Get a database import",
		Description: "Get a database import: its status, the readiness report (versions, extensions, size, primary keys, replication settings, each issue with a copy-paste fix), per-table progress and verification, replication lag, and what the destination reported. Values that were not measured are null.",
		Risk:        Read, Idempotent: true,
		Routes: []string{"GET /database-imports/{import_id}"},
	}, func(ctx context.Context, c *Call, in databaseImportInput) (*Result, error) {
		if err := checkIDs("import_id", in.ImportID); err != nil {
			return nil, err
		}
		return c.readMove(ctx, "/database-imports/"+in.ImportID, "Database import", databaseImportNext)
	})

	add(Spec{
		Name: "start_database_import", Title: "Import a database from a plan",
		Description: "Start moving one of a plan's databases into one of the user's Pantech databases (same engine, created first), through the Pantech Migrate agent running in the user's network, on any cloud: the agent reads the source with credentials that stay on it and are never sent to Pantech or through a tool. The user names the source in the agent's configuration file (database_sources, with the connection string in an environment variable, dsn_env, or a file only the agent's user can read, dsn_file), and source_ref is that name. The agent runs pg_dump or mysqldump, so it must be the agent program on a host that has them, not the container image. It checks the source against the destination first and reports what to fix. dump copies once (stop writes to the source first); replicate (PostgreSQL) keeps following changes until the cut-over. The target database must be empty. The move is free; the destination bills as usual. At most 3 imports in progress. Owners and admins only.",
		Risk:        Write,
		Routes:      []string{"POST /migrations/{migration_id}/database-imports"},
	}, func(ctx context.Context, c *Call, in startDatabaseImportInput) (*Result, error) {
		if err := checkIDs("migration_id", in.MigrationID, "destination_database_id", in.DestinationDatabaseID); err != nil {
			return nil, err
		}
		if in.ItemKey == "" {
			return nil, errorf("item_key is required: one of the plan's database items, exactly as get_migration returned it.")
		}
		if !sourceRefPattern.MatchString(in.SourceRef) {
			return nil, errorf("source_ref is the source's name in the agent's configuration: 1 to 64 letters, digits, '.', '_' and '-'. Never a connection string or password.")
		}
		if in.Mode != "" && in.Mode != "dump" && in.Mode != "replicate" {
			return nil, errorf("mode must be dump or replicate.")
		}
		body := map[string]any{"item_key": in.ItemKey, "destination_database_id": in.DestinationDatabaseID, "source_ref": in.SourceRef}
		if in.TargetDatabase != "" {
			body["target_database"] = in.TargetDatabase
		}
		if in.Mode != "" {
			body["mode"] = in.Mode
		}
		return c.writeMove(ctx, "POST", "/migrations/"+in.MigrationID+"/database-imports", body, "Database import", "The agent runs its readiness check first; follow it with get_database_import.")
	})

	for _, a := range []struct{ name, title, action, desc, next string }{
		{"check_database_import_readiness", "Check a database import's readiness again", "readiness",
			"Ask the agent to run a database import's pre-flight again, after the user fixed what it reported. Allowed before the copy starts. Owners and admins only.",
			"Follow it with get_database_import."},
		{"cut_over_database_import", "Finish a database import", "cutover",
			"Finish a database import. For dump, once every table is copied: the agent sets sequences and verifies every table. For replicate, the user stops writes to the source first: the agent applies the remaining changes until there is no lag, verifies, then the import login is removed and the replication slot dropped. Owners and admins only.",
			"Follow it with get_database_import until it is completed."},
		{"cancel_database_import", "Cancel a database import", "cancel",
			"Stop a database import and remove its access to the Pantech database; what was already copied stays. The agent drops its replication slot and publication on the source. Owners and admins only.",
			"What was copied stays in the Pantech database."},
		{"revoke_database_import_access", "Remove a database import's access", "revoke-access",
			"Remove a database import's login and its loopback-only access from the Pantech database now, keeping the data. An import still copying stops. Owners and admins only.",
			"The data stays."},
	} {
		add(Spec{
			Name: a.name, Title: a.title, Description: a.desc,
			Risk: Write, Idempotent: true,
			Routes: []string{"POST /database-imports/{import_id}/" + a.action},
		}, func(ctx context.Context, c *Call, in databaseImportInput) (*Result, error) {
			if err := checkIDs("import_id", in.ImportID); err != nil {
				return nil, err
			}
			return c.writeMove(ctx, "POST", "/database-imports/"+in.ImportID+"/"+a.action, nil, "Database import", a.next)
		})
	}

	// --- Kubernetes ---

	add(Spec{
		Name: "get_kubernetes_move_source_access", Title: "Get the source cluster's access manifests",
		Description: "Get the manifests the user applies to the source Kubernetes cluster for a move: a read-only reader (get, list, watch; no Secrets), an optional Secrets reader (only to move Secrets), and an optional copier role (only to copy volumes, bound in each namespace whose volumes are copied). They hold no secret; the source credential stays with the user's Pantech Migrate agent.",
		Risk:        Read, Idempotent: true,
		Routes: []string{"GET /kubernetes-moves/source-access"},
	}, func(ctx context.Context, c *Call, _ noInput) (*Result, error) {
		var out map[string]string
		if err := c.Client.Do(ctx, "GET", "/kubernetes-moves/source-access", nil, nil, &out); err != nil {
			return nil, err
		}
		return ok("The source cluster's access manifests are in the data: reader (always), secrets_reader (only to move Secrets) and copier (only to copy volumes). The user applies them to the source cluster with kubectl, then start_kubernetes_move.", out)
	})

	add(Spec{
		Name: "list_kubernetes_moves", Title: "List Kubernetes moves",
		Description: "List the organization's Kubernetes moves, newest first, without their resources and volumes.",
		Risk:        Read, Idempotent: true,
		Routes: []string{"GET /kubernetes-moves"},
	}, func(ctx context.Context, c *Call, _ noInput) (*Result, error) {
		return c.countList(ctx, "/kubernetes-moves", "Kubernetes move", "Kubernetes moves", "get_kubernetes_move shows one with its report.")
	})

	add(Spec{
		Name: "get_kubernetes_move", Title: "Get a Kubernetes move",
		Description: "Get a Kubernetes move with its report: every source object and what the move does with it (names only, never values; refused objects and why), the volumes and their copy progress, the images, and the workloads held scaled down until the cut-over.",
		Risk:        Read, Idempotent: true,
		Routes: []string{"GET /kubernetes-moves/{move_id}"},
	}, func(ctx context.Context, c *Call, in kubernetesMoveInput) (*Result, error) {
		if err := checkIDs("move_id", in.MoveID); err != nil {
			return nil, err
		}
		return c.readMove(ctx, "/kubernetes-moves/"+in.MoveID, "Kubernetes move", kubernetesMoveNext)
	})

	add(Spec{
		Name: "start_kubernetes_move", Title: "Start a Kubernetes move",
		Description: "Start moving a plan's kubernetes_cluster item into one of the organization's running Pantech clusters. The user's Pantech Migrate agent, enrolled for the plan's connection on any cloud, exports the source cluster with the read-only access from get_kubernetes_move_source_access, from the kubeconfig named under kubernetes in its configuration file; the source credential never leaves the agent. Volume copy pods run only Pantech's copier image (ghcr.io/pantech-dynamics/pantech-migrate-copier), which the source cluster must be able to pull. Nothing is applied yet: get_kubernetes_move shows the report, then preview_kubernetes_move and apply_kubernetes_move. Secrets move only with include_secrets, volume data only with copy_volumes. The move is free; the cluster bills as usual. At most 5 active moves, one per cluster. Owners and admins only.",
		Risk:        Write,
		Routes:      []string{"POST /migrations/{migration_id}/kubernetes-moves"},
	}, func(ctx context.Context, c *Call, in startKubernetesMoveInput) (*Result, error) {
		if err := checkIDs("migration_id", in.MigrationID, "cluster_id", in.ClusterID); err != nil {
			return nil, err
		}
		if in.ItemKey == "" {
			return nil, errorf("item_key is required: one of the plan's kubernetes_cluster items, exactly as get_migration returned it.")
		}
		body := map[string]any{"item_key": in.ItemKey, "cluster_id": in.ClusterID, "include_secrets": in.IncludeSecrets, "copy_volumes": in.CopyVolumes}
		if in.PublishLoadBalancers != nil {
			body["publish_load_balancers"] = *in.PublishLoadBalancers
		}
		if len(in.Namespaces) > 0 {
			body["namespaces"] = in.Namespaces
		}
		return c.writeMove(ctx, "POST", "/migrations/"+in.MigrationID+"/kubernetes-moves", body, "Kubernetes move", "The agent exports the source first; follow it with get_kubernetes_move.")
	})

	add(Spec{
		Name: "preview_kubernetes_move", Title: "Preview a Kubernetes move",
		Description: "Queue a server-side dry run of every object of a Kubernetes move against the target cluster. Only the move's namespaces are created (empty) so objects inside them can be checked; nothing else is. allow_privileged names workloads the cluster policy refused that the user chose to include anyway. Owners and admins only.",
		Risk:        Write,
		Routes:      []string{"POST /kubernetes-moves/{move_id}/preview"},
	}, func(ctx context.Context, c *Call, in previewKubernetesMoveInput) (*Result, error) {
		if err := checkIDs("move_id", in.MoveID); err != nil {
			return nil, err
		}
		body := map[string]any{}
		if len(in.AllowPrivileged) > 0 {
			body["allow_privileged"] = in.AllowPrivileged
		}
		return c.writeMove(ctx, "POST", "/kubernetes-moves/"+in.MoveID+"/preview", body, "Kubernetes move", "Follow it with get_kubernetes_move: each object's preview result is in the report.")
	})

	addConfirmed(Spec{
		Name: "apply_kubernetes_move", Title: "Apply a Kubernetes move",
		Description: "Apply what a Kubernetes move's last successful preview checked to the Pantech cluster, in order: definitions, namespaces, identities, configuration, storage, services, workloads, routing. LoadBalancer services become Pantech load balancers (billed, and reachable from the internet) when the move publishes them, and workloads start consuming the cluster. Workloads that mount a copied volume start scaled down until the cut-over. Owners and admins only.",
		Risk:        Exposure,
		Routes:      []string{"GET /kubernetes-moves/{move_id}", "POST /kubernetes-moves/{move_id}/apply"},
	}, func(ctx context.Context, c *Call, in applyKubernetesMoveInput) (*Preview, error) {
		if err := checkIDs("move_id", in.MoveID); err != nil {
			return nil, err
		}
		var mv struct {
			moveState
			ClusterID string `json:"cluster_id"`
			PreviewOK bool   `json:"preview_ok"`
			Options   struct {
				IncludeSecrets       bool `json:"include_secrets"`
				PublishLoadBalancers bool `json:"publish_load_balancers"`
			} `json:"options"`
			Summary struct {
				Objects int `json:"objects"`
				Refused int `json:"refused"`
				Volumes int `json:"volumes"`
			} `json:"summary"`
			AllowPrivileged []kubernetesRef `json:"allow_privileged"`
		}
		var raw json.RawMessage
		if err := c.Client.Do(ctx, "GET", "/kubernetes-moves/"+in.MoveID, nil, nil, &raw); err != nil {
			return nil, err
		}
		_ = json.Unmarshal(raw, &mv)
		p := &Preview{
			Action: fmt.Sprintf("Apply Kubernetes move %s to cluster %s: %s (%d refused by the policy and left out unless allowed).", in.MoveID, arg(mv.ClusterID), plural(mv.Summary.Objects, "object", "objects"), mv.Summary.Refused),
			Price:  "The move is free; what it runs uses the cluster's nodes, and each published load balancer is billed as a load balancer",
			Data:   map[string]any{"move": raw},
		}
		if mv.Options.PublishLoadBalancers {
			p.Warnings = append(p.Warnings, "LoadBalancer services get Pantech load balancers, reachable from the internet.")
		}
		if mv.Options.IncludeSecrets {
			p.Warnings = append(p.Warnings, "The source's Secrets are applied to the Pantech cluster.")
		}
		if len(mv.AllowPrivileged) > 0 {
			p.Warnings = append(p.Warnings, fmt.Sprintf("%s the cluster policy refused are included because the user allowed them (listed in the data).", plural(len(mv.AllowPrivileged), "workload", "workloads")))
		}
		if !mv.PreviewOK {
			p.Warnings = append(p.Warnings, "The last preview did not succeed: Pantech refuses until preview_kubernetes_move passes.")
		}
		return p, nil
	}, func(ctx context.Context, c *Call, in applyKubernetesMoveInput) (*Result, error) {
		return c.writeMove(ctx, "POST", "/kubernetes-moves/"+in.MoveID+"/apply", nil, "Kubernetes move", "Follow it with get_kubernetes_move.")
	})

	add(Spec{
		Name: "sync_kubernetes_move_volumes", Title: "Copy a Kubernetes move's volumes again",
		Description: "Queue the next copy of every volume of a Kubernetes move: only what changed since the last copy is sent, and each copy is verified by reading it back. Owners and admins only.",
		Risk:        Write,
		Routes:      []string{"POST /kubernetes-moves/{move_id}/sync-volumes"},
	}, func(ctx context.Context, c *Call, in kubernetesMoveInput) (*Result, error) {
		if err := checkIDs("move_id", in.MoveID); err != nil {
			return nil, err
		}
		return c.writeMove(ctx, "POST", "/kubernetes-moves/"+in.MoveID+"/sync-volumes", nil, "Kubernetes move", "Follow it with get_kubernetes_move.")
	})

	add(Spec{
		Name: "cut_over_kubernetes_move", Title: "Cut over a Kubernetes move",
		Description: "Once writes on the source cluster have stopped (the user says so: source_writes_stopped true), take the final copy of every volume, then scale the held workloads up to their source replicas. Owners and admins only.",
		Risk:        Write,
		Routes:      []string{"POST /kubernetes-moves/{move_id}/cutover"},
	}, func(ctx context.Context, c *Call, in cutOverKubernetesMoveInput) (*Result, error) {
		if err := checkIDs("move_id", in.MoveID); err != nil {
			return nil, err
		}
		if !in.SourceWritesStopped {
			return nil, errorf("source_writes_stopped must be true: ask the user to stop writes on the source cluster first, then call again.")
		}
		return c.writeMove(ctx, "POST", "/kubernetes-moves/"+in.MoveID+"/cutover", map[string]any{"source_writes_stopped": true}, "Kubernetes move", "Follow it with get_kubernetes_move until it is completed.")
	})

	add(Spec{
		Name: "cancel_kubernetes_move", Title: "Cancel a Kubernetes move",
		Description: "Stop a Kubernetes move and remove its copy pods on both clusters, its staged data and its export. Objects already applied to the Pantech cluster stay. Owners and admins only.",
		Risk:        Write, Idempotent: true,
		Routes: []string{"POST /kubernetes-moves/{move_id}/cancel"},
	}, func(ctx context.Context, c *Call, in kubernetesMoveInput) (*Result, error) {
		if err := checkIDs("move_id", in.MoveID); err != nil {
			return nil, err
		}
		return c.writeMove(ctx, "POST", "/kubernetes-moves/"+in.MoveID+"/cancel", nil, "Kubernetes move", "Objects already applied stay in the Pantech cluster.")
	})
}
