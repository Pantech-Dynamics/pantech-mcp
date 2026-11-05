package tools

import (
	"context"
	"fmt"

	pantech "github.com/Pantech-Dynamics/pantech-go"
)

type listKubernetesVersionsInput struct {
	ZoneID string `json:"zone_id,omitempty" jsonschema:"Only versions offered in this zone, e.g. af-abj-1."`
}

type resizeNodePlanInput struct {
	ClusterID string `json:"cluster_id" jsonschema:"The Kubernetes cluster's id."`
	NodePlan  string `json:"node_plan" jsonschema:"The plan slug the cluster's control and default worker nodes move to, from list_plans. A smaller plan works too: the nodes keep the disk they have, since a disk only grows."`
	Confirmation
}

func init() {
	add(Spec{
		Name: "list_kubernetes_versions", Title: "List Kubernetes versions",
		Description: "List the Kubernetes versions a cluster can be created with, per zone, and the smallest node plan each needs. A version's id is the kubernetes_version_id for create_kubernetes_cluster.",
		Risk:        Read, Idempotent: true,
		Routes: []string{"GET /kubernetes-versions"},
	}, func(ctx context.Context, c *Call, in listKubernetesVersionsInput) (*Result, error) {
		list, err := c.Client.Catalog.ListKubernetesVersions(ctx, &pantech.ListKubernetesVersionsParams{ZoneID: in.ZoneID})
		if err != nil {
			return nil, err
		}
		return ok(plural(len(list.Data), "version", "versions")+".", list)
	})

	addConfirmed(Spec{
		Name: "resize_kubernetes_node_plan", Title: "Resize a cluster's nodes",
		Description: "Move a Kubernetes cluster's control node and default workers to another plan. Nodes are resized one at a time: each worker is drained, every node is restarted on the new plan and the next waits until it is Ready again. The cluster is billed at its current plan until every node runs the new one. Node pools keep their own plans.",
		Risk:        Cost, Idempotent: true,
		Routes: []string{"GET /kubernetes-clusters/{cluster_id}", "GET /plans", "GET /billing/credit", "POST /pricing/estimate",
			"POST /kubernetes-clusters/{cluster_id}/resize-plan", "GET /operations/{operation_id}"},
	}, func(ctx context.Context, c *Call, in resizeNodePlanInput) (*Preview, error) {
		if err := checkIDs("cluster_id", in.ClusterID); err != nil {
			return nil, err
		}
		cl, err := c.Client.Kubernetes.GetCluster(ctx, in.ClusterID)
		if err != nil {
			return nil, err
		}
		placement := placementOf(str(cl.SubnetID), "")
		plan, err := c.findPlan(ctx, in.NodePlan, placement)
		if err != nil {
			return nil, err
		}
		nodes := cl.ControlNodes + cl.Workers
		p := &Preview{
			Action: fmt.Sprintf("Resize the %d nodes of cluster %s to plan %s (%d vCPU, %d MB memory, %d GB disk), one node at a time.",
				nodes, cl.ID, plan.Slug, plan.VCPU, plan.MemoryMB, plan.DiskGB),
			Price: c.nodesPrice(ctx, plan.Slug, placement, nodes) + " once every node runs the new plan",
			Warnings: []string{
				"Each node restarts: a cluster with one worker cannot run pods while it is resized, and the cluster's API is unavailable for a few minutes while the control node restarts.",
				"A node's disk only grows.",
			},
			Data: map[string]any{"cluster": cl, "new_plan": plan},
		}
		disk := cl.Node.DiskGB // the nodes' disk, which can be bigger than their plan's
		if cur := c.planByID(ctx, cl.NodePlanID, placement); cur != nil {
			p.Price += fmt.Sprintf("; now %s", c.nodesPrice(ctx, cur.Slug, placement, nodes))
			disk = max(disk, cur.DiskGB)
		}
		if plan.DiskGB < disk {
			p.Warnings = append(p.Warnings, fmt.Sprintf("Plan %s has a %d GB disk and the nodes have %d GB: they take its vCPU and memory and keep their %d GB disk.", plan.Slug, plan.DiskGB, disk, disk))
		}
		return p, nil
	}, func(ctx context.Context, c *Call, in resizeNodePlanInput) (*Result, error) {
		acc, err := c.Client.Kubernetes.ResizePlan(ctx, in.ClusterID, pantech.ResizeKubernetesClusterPlanRequest{NodePlan: in.NodePlan}, c.writeOpts()...)
		if err != nil {
			return nil, err
		}
		return c.finish(ctx, acc, fmt.Sprintf("cluster %s's nodes are moving to %s", in.ClusterID, in.NodePlan))
	})
}
