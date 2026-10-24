package tools

import (
	"context"
	"fmt"

	pantech "github.com/Pantech-Dynamics/pantech-go"
)

type createVolumeInput struct {
	Name             string `json:"name" jsonschema:"1 to 255 characters."`
	DiskOfferingSlug string `json:"disk_offering_slug" jsonschema:"The volume type, from the disk offerings (e.g. custom)."`
	SizeGB           int    `json:"size_gb,omitempty" jsonschema:"Size in GB, for a custom-size offering."`
	Region           string `json:"region,omitempty" jsonschema:"A region code from list_regions."`
	InstanceID       string `json:"instance_id,omitempty" jsonschema:"Attach it at once to this running instance in the same zone."`
	MountPoint       string `json:"mount_point,omitempty" jsonschema:"With instance_id: an absolute path to mount it at, e.g. /data."`
	Encrypted        bool   `json:"encrypted,omitempty" jsonschema:"Encrypt the volume."`
	Confirmation
}

type volumeInput struct {
	VolumeID string `json:"volume_id" jsonschema:"The volume's id."`
}

type attachVolumeInput struct {
	VolumeID   string `json:"volume_id" jsonschema:"The volume's id."`
	InstanceID string `json:"instance_id" jsonschema:"The instance to attach it to, in the same zone."`
}

type resizeVolumeInput struct {
	VolumeID         string `json:"volume_id" jsonschema:"The volume's id."`
	DiskOfferingSlug string `json:"disk_offering_slug" jsonschema:"The offering to move to (the current one for a custom-size volume)."`
	SizeGB           int    `json:"size_gb,omitempty" jsonschema:"The new size in GB, for a custom-size offering. Volumes only grow."`
	Confirmation
}

type deleteVolumeInput struct {
	VolumeID string `json:"volume_id" jsonschema:"The volume's id. Detach it first."`
	Confirmation
}

func init() {
	add(Spec{
		Name: "list_volumes", Title: "List volumes",
		Description: "List the organization's block storage volumes, their size, monthly cost and which instance they are attached to.",
		Risk:        Read, Idempotent: true,
		Routes: []string{"GET /volumes"},
	}, func(ctx context.Context, c *Call, in listInput) (*Result, error) {
		page, err := c.Client.Volumes.List(ctx, in.params())
		if err != nil {
			return nil, err
		}
		return pageResult(page, "volume", "volumes")
	})

	addConfirmed(Spec{
		Name: "create_volume", Title: "Create a volume",
		Description: "Create a block storage volume, optionally attached at once to an instance. Billed hourly by size until deleted.",
		Risk:        Cost,
		Routes:      []string{"GET /disk-offerings/{slug}/quote", "POST /volumes", "GET /operations/{operation_id}"},
	}, func(ctx context.Context, c *Call, in createVolumeInput) (*Preview, error) {
		if err := checkIDs("disk_offering_slug", in.DiskOfferingSlug); err != nil {
			return nil, err
		}
		if err := checkOptionalIDs("instance_id", in.InstanceID); err != nil {
			return nil, err
		}
		p := &Preview{
			Action: fmt.Sprintf("Create a %s volume (offering %s).", sizeText(in.SizeGB), in.DiskOfferingSlug),
			Data:   map[string]any{"request": in.request()},
		}
		if in.InstanceID != "" {
			p.Action = fmt.Sprintf("Create a %s volume (offering %s) and attach it to instance %s.", sizeText(in.SizeGB), in.DiskOfferingSlug, in.InstanceID)
		}
		p.Price = c.diskQuote(ctx, in.DiskOfferingSlug, in.SizeGB)
		return p, nil
	}, func(ctx context.Context, c *Call, in createVolumeInput) (*Result, error) {
		acc, err := c.Client.Volumes.Create(ctx, in.request(), c.writeOpts()...)
		if err != nil {
			return nil, err
		}
		return c.finish(ctx, acc, "volume "+acc.ResourceID+" created")
	})

	add(Spec{
		Name: "attach_volume", Title: "Attach a volume",
		Description: "Attach a volume to an instance in the same zone.",
		Risk:        Write, Idempotent: true,
		Routes: []string{"POST /volumes/{volume_id}/attach", "GET /operations/{operation_id}"},
	}, func(ctx context.Context, c *Call, in attachVolumeInput) (*Result, error) {
		if err := checkIDs("volume_id", in.VolumeID, "instance_id", in.InstanceID); err != nil {
			return nil, err
		}
		acc, err := c.Client.Volumes.Attach(ctx, in.VolumeID, pantech.AttachVolumeRequest{InstanceID: in.InstanceID}, c.writeOpts()...)
		if err != nil {
			return nil, err
		}
		return c.finish(ctx, acc, fmt.Sprintf("volume %s attached to instance %s", in.VolumeID, in.InstanceID))
	})

	add(Spec{
		Name: "detach_volume", Title: "Detach a volume",
		Description: "Detach a volume from its instance. Tell the user to unmount it inside the instance first, or data being written may be lost.",
		Risk:        Write, Idempotent: true,
		Routes: []string{"POST /volumes/{volume_id}/detach", "GET /operations/{operation_id}"},
	}, func(ctx context.Context, c *Call, in volumeInput) (*Result, error) {
		if err := checkIDs("volume_id", in.VolumeID); err != nil {
			return nil, err
		}
		acc, err := c.Client.Volumes.Detach(ctx, in.VolumeID, c.writeOpts()...)
		if err != nil {
			return nil, err
		}
		return c.finish(ctx, acc, "volume "+in.VolumeID+" detached")
	})

	addConfirmed(Spec{
		Name: "resize_volume", Title: "Grow a volume",
		Description: "Grow a volume to a bigger offering or size. Volumes only grow; extend the file system inside the instance afterwards. The larger size is billed hourly.",
		Risk:        Cost, Idempotent: true,
		Routes: []string{"GET /volumes/{volume_id}", "GET /disk-offerings/{slug}/quote", "POST /volumes/{volume_id}/resize", "GET /operations/{operation_id}"},
	}, func(ctx context.Context, c *Call, in resizeVolumeInput) (*Preview, error) {
		if err := checkIDs("volume_id", in.VolumeID, "disk_offering_slug", in.DiskOfferingSlug); err != nil {
			return nil, err
		}
		vol, err := c.Client.Volumes.Get(ctx, in.VolumeID)
		if err != nil {
			return nil, err
		}
		p := &Preview{
			Action:   fmt.Sprintf("Grow volume %s from %d GB to %s (offering %s).", vol.ID, vol.SizeGB, sizeText(in.SizeGB), in.DiskOfferingSlug),
			Price:    c.diskQuote(ctx, in.DiskOfferingSlug, in.SizeGB),
			Warnings: []string{"Volumes only grow: this cannot be undone. Extend the file system inside the instance afterwards."},
			Data:     map[string]any{"volume": vol},
		}
		if vol.MonthlyCost != nil {
			p.Price += "; now " + money(vol.MonthlyCost.AmountMinor, vol.MonthlyCost.Currency) + " a month"
		}
		return p, nil
	}, func(ctx context.Context, c *Call, in resizeVolumeInput) (*Result, error) {
		req := pantech.ResizeVolumeRequest{DiskOfferingSlug: in.DiskOfferingSlug}
		if in.SizeGB > 0 {
			req.SizeGB = pantech.Ptr(in.SizeGB)
		}
		acc, err := c.Client.Volumes.Resize(ctx, in.VolumeID, req, c.writeOpts()...)
		if err != nil {
			return nil, err
		}
		return c.finish(ctx, acc, "volume "+in.VolumeID+" grown")
	})

	addConfirmed(Spec{
		Name: "delete_volume", Title: "Delete a volume",
		Description: "Delete a volume and all its data. Detach it first. Its snapshots are kept (and stay billed). Needs a key with delete access.",
		Risk:        Destructive, Destructive: true, Idempotent: true,
		Routes: []string{"GET /volumes/{volume_id}", "DELETE /volumes/{volume_id}", "GET /operations/{operation_id}"},
	}, func(ctx context.Context, c *Call, in deleteVolumeInput) (*Preview, error) {
		if err := checkIDs("volume_id", in.VolumeID); err != nil {
			return nil, err
		}
		vol, err := c.Client.Volumes.Get(ctx, in.VolumeID)
		if err != nil {
			return nil, err
		}
		p := &Preview{
			Action:   fmt.Sprintf("Permanently delete volume %s (%d GB) and every file on it. This cannot be undone.", vol.ID, vol.SizeGB),
			Warnings: []string{"Its snapshots are kept and stay billed until deleted."},
			Data:     map[string]any{"volume": vol},
		}
		if vol.AttachedInstanceID != nil {
			p.Warnings = append(p.Warnings, "It is attached to instance "+*vol.AttachedInstanceID+": detach it first, or the delete is refused.")
		}
		return p, nil
	}, func(ctx context.Context, c *Call, in deleteVolumeInput) (*Result, error) {
		acc, err := c.Client.Volumes.Delete(ctx, in.VolumeID, c.writeOpts()...)
		if err != nil {
			return nil, err
		}
		return c.finish(ctx, acc, "volume "+in.VolumeID+" deleted")
	})
}

func (in createVolumeInput) request() pantech.CreateVolumeRequest {
	req := pantech.CreateVolumeRequest{Name: in.Name, DiskOfferingSlug: in.DiskOfferingSlug}
	if in.SizeGB > 0 {
		req.SizeGB = pantech.Ptr(in.SizeGB)
	}
	if in.Region != "" {
		req.Region = pantech.Ptr(in.Region)
	}
	if in.InstanceID != "" {
		req.InstanceID = pantech.Ptr(in.InstanceID)
	}
	if in.MountPoint != "" {
		req.MountPoint = pantech.Ptr(in.MountPoint)
	}
	if in.Encrypted {
		req.Encrypted = pantech.Ptr(true)
	}
	return req
}

func sizeText(gb int) string {
	if gb <= 0 {
		return "standard-size"
	}
	return fmt.Sprintf("%d GB", gb)
}

// diskQuote prices a disk offering at a size, or says the API gave no quote.
func (c *Call) diskQuote(ctx context.Context, slug string, sizeGB int) string {
	var params *pantech.DiskOfferingQuoteParams
	if sizeGB > 0 {
		params = &pantech.DiskOfferingQuoteParams{SizeGB: sizeGB}
	}
	q, err := c.Client.Catalog.QuoteDiskOffering(ctx, slug, params)
	if err != nil {
		return "no quote available (check the offering slug and size)"
	}
	return fmt.Sprintf("about %s a month (%s an hour) for %d GB", money(q.MonthlyPriceMinor, q.Currency), money(q.HourlyPriceMinor, q.Currency), q.SizeGB)
}
