package tools

import (
	"context"
	"fmt"

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
		p.Price = "billed hourly on the snapshot's stored size"
		if sizeGB > 0 {
			if q, err := c.Client.Snapshots.Quote(ctx, sizeGB); err == nil && q.Priced && q.MonthlyMinor != nil && q.Currency != nil {
				p.Price = fmt.Sprintf("at most about %s a month if it stores the full %d GB (billed on stored size)", money(*q.MonthlyMinor, *q.Currency), sizeGB)
			}
		}
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
