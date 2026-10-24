package tools

import (
	"context"
	"fmt"
	"strconv"

	pantech "github.com/Pantech-Dynamics/pantech-go"
)

type createSnapshotInput struct {
	Name       string `json:"name" jsonschema:"The snapshot's name, unique among the source's snapshots."`
	InstanceID string `json:"instance_id,omitempty" jsonschema:"Snapshot this instance's root disk. Give exactly one of instance_id and volume_id."`
	VolumeID   string `json:"volume_id,omitempty" jsonschema:"Snapshot this volume."`
	Confirmation
}

func init() {
	add(Spec{
		Name: "list_snapshots", Title: "List snapshots",
		Description: "List the organization's snapshots of instances, volumes and databases, with their size and state.",
		Risk:        Read, Idempotent: true,
		Routes: []string{"GET /snapshots"},
	}, func(ctx context.Context, c *Call, in listInput) (*Result, error) {
		page, err := c.Client.Snapshots.List(ctx, in.params())
		if err != nil {
			return nil, err
		}
		return pageResult(page, "snapshot", "snapshots")
	})

	addConfirmed(Spec{
		Name: "create_snapshot", Title: "Create a snapshot",
		Description: "Take a crash-consistent snapshot of an instance's root disk or of a volume. Billed hourly on its stored size until deleted.",
		Risk:        Cost,
		Routes: []string{"GET /instances/{instance_id}", "GET /volumes/{volume_id}", "GET /snapshots/quote",
			"POST /instances/{instance_id}/snapshots", "POST /volumes/{volume_id}/snapshots", "GET /operations/{operation_id}"},
	}, func(ctx context.Context, c *Call, in createSnapshotInput) (*Preview, error) {
		if (in.InstanceID == "") == (in.VolumeID == "") {
			return nil, errorf("Give exactly one of instance_id and volume_id.")
		}
		if err := checkOptionalIDs("instance_id", in.InstanceID, "volume_id", in.VolumeID); err != nil {
			return nil, err
		}
		var sizeGB int
		var p Preview
		if in.InstanceID != "" {
			vm, err := c.Client.Instances.Get(ctx, in.InstanceID)
			if err != nil {
				return nil, err
			}
			if vm.Spec != nil {
				sizeGB = vm.Spec.DiskGB
			}
			p.Action = fmt.Sprintf("Take a snapshot of the root disk of instance %s.", vm.ID)
			p.Data = map[string]any{"instance": vm, "name": in.Name}
		} else {
			vol, err := c.Client.Volumes.Get(ctx, in.VolumeID)
			if err != nil {
				return nil, err
			}
			sizeGB = vol.SizeGB
			p.Action = fmt.Sprintf("Take a snapshot of volume %s (%d GB).", vol.ID, vol.SizeGB)
			p.Data = map[string]any{"volume": vol, "name": in.Name}
		}
		p.Price = c.snapshotPrice(ctx, sizeGB)
		return &p, nil
	}, func(ctx context.Context, c *Call, in createSnapshotInput) (*Result, error) {
		req := pantech.CreateSnapshotRequest{Name: in.Name}
		var acc *pantech.Accepted
		var err error
		if in.InstanceID != "" {
			acc, err = c.Client.Instances.CreateSnapshot(ctx, in.InstanceID, req, c.writeOpts()...)
		} else {
			acc, err = c.Client.Volumes.CreateSnapshot(ctx, in.VolumeID, req, c.writeOpts()...)
		}
		if err != nil {
			return nil, err
		}
		return c.finish(ctx, acc, "snapshot "+acc.ResourceID+" taken")
	})
}

type snapshotInput struct {
	SnapshotID string `json:"snapshot_id" jsonschema:"The snapshot's id, e.g. snap_…"`
	Confirmation
}

type restoreSnapshotInput struct {
	SnapshotID       string `json:"snapshot_id" jsonschema:"An instance or volume snapshot (database snapshots cannot be restored yet)."`
	Name             string `json:"name" jsonschema:"The new volume's name, 1 to 255 characters."`
	DiskOfferingSlug string `json:"disk_offering_slug" jsonschema:"The new volume's type, from the disk offerings (e.g. custom)."`
	SizeGB           int    `json:"size_gb,omitempty" jsonschema:"For a custom-size offering: at least the snapshot's size in GB."`
	Confirmation
}

type scheduleTargetInput struct {
	InstanceID string `json:"instance_id,omitempty" jsonschema:"The instance whose root disk is snapshotted. Give exactly one of instance_id and volume_id."`
	VolumeID   string `json:"volume_id,omitempty" jsonschema:"The volume snapshotted."`
}

type setSnapshotScheduleInput struct {
	InstanceID     string `json:"instance_id,omitempty" jsonschema:"The instance whose root disk is snapshotted. Give exactly one of instance_id and volume_id."`
	VolumeID       string `json:"volume_id,omitempty" jsonschema:"The volume snapshotted."`
	Frequency      string `json:"frequency" jsonschema:"daily, weekly or monthly."`
	RetentionCount int    `json:"retention_count,omitempty" jsonschema:"How many successful automatic snapshots to keep, 1 to 168 (default 7). Older automatic ones are deleted: lowering it deletes snapshots."`
	Enabled        *bool  `json:"enabled,omitempty" jsonschema:"false pauses the schedule without forgetting it (default true)."`
	Confirmation
}

func (in scheduleTargetInput) check() error {
	if (in.InstanceID == "") == (in.VolumeID == "") {
		return errorf("Give exactly one of instance_id and volume_id.")
	}
	return checkOptionalIDs("instance_id", in.InstanceID, "volume_id", in.VolumeID)
}

func (in scheduleTargetInput) what() string {
	if in.InstanceID != "" {
		return "the root disk of instance " + in.InstanceID
	}
	return "volume " + in.VolumeID
}

func (c *Call) getSchedule(ctx context.Context, in scheduleTargetInput) (*pantech.SnapshotSchedule, error) {
	if in.InstanceID != "" {
		return c.Client.Instances.GetSnapshotSchedule(ctx, in.InstanceID)
	}
	return c.Client.Volumes.GetSnapshotSchedule(ctx, in.VolumeID)
}

func init() {
	addConfirmed(Spec{
		Name: "delete_snapshot", Title: "Delete a snapshot",
		Description: "Delete a snapshot of an instance, volume or database. The copy it holds is lost for good. It is billed until the deletion completes. Needs an AI app connected with Manage and delete access.",
		Risk:        Destructive, Destructive: true, Idempotent: true,
		Routes: []string{"GET /snapshots/{snapshot_id}", "DELETE /snapshots/{snapshot_id}", "GET /operations/{operation_id}"},
	}, func(ctx context.Context, c *Call, in snapshotInput) (*Preview, error) {
		if err := checkIDs("snapshot_id", in.SnapshotID); err != nil {
			return nil, err
		}
		snap, err := c.Client.Snapshots.Get(ctx, in.SnapshotID)
		if err != nil {
			return nil, err
		}
		return &Preview{
			Action:   fmt.Sprintf("Permanently delete snapshot %s (%s stored). The data it holds is lost and cannot be restored afterwards.", snap.ID, gbText(snap.SizeBytes)),
			Warnings: []string{deleteAccessNote},
			Data:     map[string]any{"snapshot": snap},
		}, nil
	}, func(ctx context.Context, c *Call, in snapshotInput) (*Result, error) {
		acc, err := c.Client.Snapshots.Delete(ctx, in.SnapshotID, c.writeOpts()...)
		if err != nil {
			return nil, err
		}
		return c.finish(ctx, acc, "snapshot "+in.SnapshotID+" deleted")
	})

	addConfirmed(Spec{
		Name: "restore_snapshot", Title: "Restore a snapshot to a new volume",
		Description: "Create a NEW volume from an instance or volume snapshot (nothing existing is overwritten). The volume is billed hourly by size until deleted. To roll an instance back instead, use restore_instance_snapshot.",
		Risk:        Cost,
		Routes:      []string{"GET /snapshots/{snapshot_id}", "GET /disk-offerings/{slug}/quote", "POST /snapshots/{snapshot_id}/restore", "GET /operations/{operation_id}"},
	}, func(ctx context.Context, c *Call, in restoreSnapshotInput) (*Preview, error) {
		if err := checkIDs("snapshot_id", in.SnapshotID, "disk_offering_slug", in.DiskOfferingSlug); err != nil {
			return nil, err
		}
		snap, err := c.Client.Snapshots.Get(ctx, in.SnapshotID)
		if err != nil {
			return nil, err
		}
		return &Preview{
			Action: fmt.Sprintf("Create a new %s volume (offering %s) from snapshot %s.", sizeText(in.SizeGB), in.DiskOfferingSlug, snap.ID),
			Price:  c.diskQuote(ctx, in.DiskOfferingSlug, in.SizeGB),
			Data:   map[string]any{"snapshot": snap, "request": in.request()},
		}, nil
	}, func(ctx context.Context, c *Call, in restoreSnapshotInput) (*Result, error) {
		acc, err := c.Client.Snapshots.Restore(ctx, in.SnapshotID, in.request(), c.writeOpts()...)
		if err != nil {
			return nil, err
		}
		return c.finish(ctx, acc, "volume "+acc.ResourceID+" created from snapshot "+in.SnapshotID)
	})

	add(Spec{
		Name: "get_snapshot_schedule", Title: "Get a snapshot schedule",
		Description: "Get the automatic snapshot schedule of an instance's root disk or of a volume: how often, how many are kept, and when the next runs.",
		Risk:        Read, Idempotent: true,
		Routes: []string{"GET /instances/{instance_id}/snapshot-schedule", "GET /volumes/{volume_id}/snapshot-schedule"},
	}, func(ctx context.Context, c *Call, in scheduleTargetInput) (*Result, error) {
		if err := in.check(); err != nil {
			return nil, err
		}
		s, err := c.getSchedule(ctx, in)
		if err != nil {
			return nil, err
		}
		state := "on"
		if !s.Enabled {
			state = "paused"
		}
		return ok(fmt.Sprintf("Automatic snapshots of %s: %s, %s, keeping %d.", in.what(), arg(s.Frequency), state, s.RetentionCount), s)
	})

	addConfirmed(Spec{
		Name: "set_snapshot_schedule", Title: "Set a snapshot schedule",
		Description: "Turn on, change or pause automatic snapshots of an instance's root disk or a volume. Automatic snapshots are billed like manual ones. Lowering retention_count deletes the oldest automatic snapshots, which needs an AI app connected with Manage and delete access.",
		Risk:        Destructive, Destructive: true, Idempotent: true,
		Routes: []string{"GET /instances/{instance_id}/snapshot-schedule", "GET /volumes/{volume_id}/snapshot-schedule",
			"PUT /instances/{instance_id}/snapshot-schedule", "PUT /volumes/{volume_id}/snapshot-schedule"},
	}, func(ctx context.Context, c *Call, in setSnapshotScheduleInput) (*Preview, error) {
		target := scheduleTargetInput{InstanceID: in.InstanceID, VolumeID: in.VolumeID}
		if err := target.check(); err != nil {
			return nil, err
		}
		req := in.request()
		keep := 7
		if req.RetentionCount != nil {
			keep = *req.RetentionCount
		}
		p := &Preview{
			Action: fmt.Sprintf("Take automatic %s snapshots of %s, keeping the newest %d.", arg(in.Frequency), target.what(), keep),
			Price:  "each automatic snapshot is billed hourly on its stored size, like a manual one",
			Data:   map[string]any{"request": req},
		}
		if in.Enabled != nil && !*in.Enabled {
			p.Action = fmt.Sprintf("Pause automatic snapshots of %s (kept: %d, %s).", target.what(), keep, arg(in.Frequency))
		}
		if cur, err := c.getSchedule(ctx, target); err == nil && cur.RetentionCount > 0 {
			p.Data = map[string]any{"current": cur, "request": req}
			if keep < cur.RetentionCount {
				p.Warnings = append(p.Warnings,
					fmt.Sprintf("Lowering retention from %d to %d DELETES the oldest automatic snapshots beyond the newest %d: their data is lost.", cur.RetentionCount, keep, keep),
					deleteAccessNote)
			}
		}
		return p, nil
	}, func(ctx context.Context, c *Call, in setSnapshotScheduleInput) (*Result, error) {
		var s *pantech.SnapshotSchedule
		var err error
		if in.InstanceID != "" {
			s, err = c.Client.Instances.PutSnapshotSchedule(ctx, in.InstanceID, in.request(), c.writeOpts()...)
		} else {
			s, err = c.Client.Volumes.PutSnapshotSchedule(ctx, in.VolumeID, in.request(), c.writeOpts()...)
		}
		if err != nil {
			return nil, err
		}
		return ok(fmt.Sprintf("Saved: automatic snapshots of %s, %s, keeping %d.", scheduleTargetInput{InstanceID: in.InstanceID, VolumeID: in.VolumeID}.what(), arg(s.Frequency), s.RetentionCount), s)
	})

	add(Spec{
		Name: "get_volume_usage", Title: "Get volume usage",
		Description: "Measured storage usage of one volume in the current billing period. These are measured quantities: report them exactly as given.",
		Risk:        Read, Idempotent: true,
		Routes: []string{"GET /volumes/{volume_id}/usage"},
	}, func(ctx context.Context, c *Call, in volumeInput) (*Result, error) {
		if err := checkIDs("volume_id", in.VolumeID); err != nil {
			return nil, err
		}
		u, err := c.Client.Volumes.GetUsage(ctx, in.VolumeID)
		if err != nil {
			return nil, err
		}
		return usageResult("volume "+in.VolumeID, u)
	})
}

func (in restoreSnapshotInput) request() pantech.RestoreSnapshotRequest {
	req := pantech.RestoreSnapshotRequest{Name: in.Name, DiskOfferingSlug: in.DiskOfferingSlug}
	if in.SizeGB > 0 {
		req.SizeGB = pantech.Ptr(in.SizeGB)
	}
	return req
}

func (in setSnapshotScheduleInput) request() pantech.PutSnapshotScheduleRequest {
	req := pantech.PutSnapshotScheduleRequest{Frequency: in.Frequency, Enabled: in.Enabled}
	if in.RetentionCount > 0 {
		req.RetentionCount = pantech.Ptr(in.RetentionCount)
	}
	return req
}

// gbText shows a stored size in GB, exactly enough for a preview.
func gbText(bytes int64) string {
	if bytes <= 0 {
		return "size not known yet"
	}
	return strconv.FormatFloat(float64(bytes)/(1<<30), 'f', 2, 64) + " GB"
}
