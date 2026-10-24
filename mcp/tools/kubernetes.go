package tools

import (
	"context"
	"fmt"

	pantech "github.com/Pantech-Dynamics/pantech-go"
)

type clusterInput struct {
	ClusterID string `json:"cluster_id" jsonschema:"The Kubernetes cluster's id."`
}

type createClusterInput struct {
	Name                string   `json:"name" jsonschema:"1 to 40 lowercase letters, digits and hyphens, starting with a letter."`
	ZoneID              string   `json:"zone_id" jsonschema:"The zone, e.g. af-abj-1."`
	KubernetesVersionID string   `json:"kubernetes_version_id" jsonschema:"A Kubernetes version id offered in the zone."`
	NodePlan            string   `json:"node_plan" jsonschema:"The plan slug every node uses, from list_plans."`
	ControlNodes        int      `json:"control_nodes,omitempty" jsonschema:"1 (default) or 3 for a highly available control plane where offered."`
	Workers             int      `json:"workers,omitempty" jsonschema:"Worker nodes, 1 to 10."`
	SubnetID            string   `json:"subnet_id,omitempty" jsonschema:"Required in a VPC zone: the subnet the nodes go in."`
	APIAllowedCIDRs     []string `json:"api_allowed_cidrs,omitempty" jsonschema:"VPC zone only: IPv4 CIDRs allowed to reach the API server."`
	EndpointAccess      string   `json:"endpoint_access,omitempty" jsonschema:"Who can reach the API server: private, public or dual."`
	Confirmation
}

type scaleNodePoolInput struct {
	ClusterID          string `json:"cluster_id" jsonschema:"The cluster's id."`
	PoolID             string `json:"pool_id" jsonschema:"The node pool's id, from get_kubernetes_cluster."`
	Workers            *int   `json:"workers,omitempty" jsonschema:"The new worker count."`
	AutoscalingEnabled *bool  `json:"autoscaling_enabled,omitempty" jsonschema:"Turn autoscaling on or off."`
	MinWorkers         *int   `json:"min_workers,omitempty" jsonschema:"Autoscaling minimum."`
	MaxWorkers         *int   `json:"max_workers,omitempty" jsonschema:"Autoscaling maximum."`
	Confirmation
}

func init() {
	add(Spec{
		Name: "list_kubernetes_clusters", Title: "List Kubernetes clusters",
		Description: "List the organization's managed Kubernetes clusters with their version, node counts and state.",
		Risk:        Read, Idempotent: true,
		Routes: []string{"GET /kubernetes-clusters"},
	}, func(ctx context.Context, c *Call, _ noInput) (*Result, error) {
		clusters, err := c.Client.Kubernetes.ListClusters(ctx)
		if err != nil {
			return nil, err
		}
		return ok(plural(len(clusters), "cluster", "clusters")+".", clusters)
	})

	add(Spec{
		Name: "get_kubernetes_cluster", Title: "Get a Kubernetes cluster",
		Description: "Get one Kubernetes cluster and its node pools (pool ids are what scale_node_pool takes). Never returns a kubeconfig.",
		Risk:        Read, Idempotent: true,
		Routes: []string{"GET /kubernetes-clusters/{cluster_id}", "GET /kubernetes-clusters/{cluster_id}/node-pools"},
	}, func(ctx context.Context, c *Call, in clusterInput) (*Result, error) {
		if err := checkIDs("cluster_id", in.ClusterID); err != nil {
			return nil, err
		}
		cl, err := c.Client.Kubernetes.GetCluster(ctx, in.ClusterID)
		if err != nil {
			return nil, err
		}
		pools, err := c.Client.Kubernetes.ListNodePools(ctx, in.ClusterID)
		if err != nil {
			return nil, err
		}
		return ok(fmt.Sprintf("Cluster %s runs Kubernetes %s with %d control and %d worker nodes; it is %s. %s.",
			cl.ID, cl.KubernetesVersion, cl.ControlNodes, cl.Workers, cl.ObservedState, plural(len(pools), "node pool", "node pools")),
			map[string]any{"cluster": cl, "node_pools": pools})
	})

	addConfirmed(Spec{
		Name: "create_kubernetes_cluster", Title: "Create a Kubernetes cluster",
		Description: "Create a managed Kubernetes cluster. Every node is billed as an instance of node_plan.",
		Risk:        Cost,
		Routes:      []string{"GET /plans", "GET /billing/credit", "POST /pricing/estimate", "POST /kubernetes-clusters", "GET /operations/{operation_id}"},
	}, func(ctx context.Context, c *Call, in createClusterInput) (*Preview, error) {
		control, workers := in.ControlNodes, in.Workers
		if control == 0 {
			control = 1
		}
		if workers == 0 {
			workers = 1
		}
		p := &Preview{
			Action: fmt.Sprintf("Create a Kubernetes cluster in zone %s with %d control and %d worker nodes on plan %s.", arg(in.ZoneID), control, workers, arg(in.NodePlan)),
			Data:   map[string]any{"request": in.request()},
		}
		p.Price = c.nodesPrice(ctx, in.NodePlan, placementOf(in.SubnetID, ""), control+workers)
		return p, nil
	}, func(ctx context.Context, c *Call, in createClusterInput) (*Result, error) {
		acc, err := c.Client.Kubernetes.CreateCluster(ctx, in.request(), c.writeOpts()...)
		if err != nil {
			return nil, err
		}
		return c.finish(ctx, acc, "Kubernetes cluster "+acc.ResourceID+" created")
	})

	addConfirmed(Spec{
		Name: "scale_node_pool", Title: "Scale a node pool",
		Description: "Change a Kubernetes node pool's worker count and autoscaling. Adding workers adds billed nodes; removing them drains and deletes nodes.",
		Risk:        Cost, Destructive: true, Idempotent: true,
		Routes: []string{"GET /kubernetes-clusters/{cluster_id}", "GET /kubernetes-clusters/{cluster_id}/node-pools", "GET /plans", "GET /billing/credit", "POST /pricing/estimate",
			"PATCH /kubernetes-clusters/{cluster_id}/node-pools/{pool_id}", "GET /operations/{operation_id}"},
	}, func(ctx context.Context, c *Call, in scaleNodePoolInput) (*Preview, error) {
		if err := checkIDs("cluster_id", in.ClusterID, "pool_id", in.PoolID); err != nil {
			return nil, err
		}
		if in.Workers == nil && in.AutoscalingEnabled == nil && in.MinWorkers == nil && in.MaxWorkers == nil {
			return nil, errorf("Nothing to change: give workers, or the autoscaling settings.")
		}
		cl, err := c.Client.Kubernetes.GetCluster(ctx, in.ClusterID)
		if err != nil {
			return nil, err
		}
		pools, err := c.Client.Kubernetes.ListNodePools(ctx, in.ClusterID)
		if err != nil {
			return nil, err
		}
		var pool *pantech.KubernetesNodePool
		for i := range pools {
			if pools[i].ID == in.PoolID {
				pool = &pools[i]
			}
		}
		if pool == nil {
			return nil, errorf("Cluster %s has no node pool %s. Call get_kubernetes_cluster for its pools.", in.ClusterID, in.PoolID)
		}
		p := &Preview{Data: map[string]any{"node_pool": pool, "request": in.request()}}
		if in.Workers != nil {
			p.Action = fmt.Sprintf("Scale node pool %s of cluster %s from %d to %d workers.", pool.ID, cl.ID, pool.Workers, *in.Workers)
			if *in.Workers < pool.Workers {
				p.Warnings = append(p.Warnings, "Removed nodes are drained and deleted; their pods are rescheduled.")
			}
			p.Price = "each worker is billed as an instance of the pool's plan"
			if plan := c.planByID(ctx, pool.NodePlanID, placementOf(str(cl.SubnetID), "")); plan != nil {
				p.Price = fmt.Sprintf("%s at %d workers (each %s)", c.nodesPrice(ctx, plan.Slug, placementOf(str(cl.SubnetID), ""), *in.Workers), *in.Workers, planPrice(plan))
			}
		} else {
			p.Action = fmt.Sprintf("Change the autoscaling of node pool %s of cluster %s.", pool.ID, cl.ID)
			p.Price = "autoscaling adds and removes billed workers between min_workers and max_workers"
		}
		return p, nil
	}, func(ctx context.Context, c *Call, in scaleNodePoolInput) (*Result, error) {
		acc, err := c.Client.Kubernetes.ScaleNodePool(ctx, in.ClusterID, in.PoolID, in.request(), c.writeOpts()...)
		if err != nil {
			return nil, err
		}
		return c.finish(ctx, acc, "node pool "+in.PoolID+" scaled")
	})
}

func (in createClusterInput) request() pantech.CreateKubernetesClusterRequest {
	req := pantech.CreateKubernetesClusterRequest{Name: in.Name, ZoneID: in.ZoneID, KubernetesVersionID: in.KubernetesVersionID, NodePlan: in.NodePlan}
	if in.ControlNodes != 0 {
		req.ControlNodes = pantech.Ptr(in.ControlNodes)
	}
	if in.Workers != 0 {
		req.Workers = pantech.Ptr(in.Workers)
	}
	if in.SubnetID != "" {
		req.SubnetID = pantech.Ptr(in.SubnetID)
	}
	if len(in.APIAllowedCIDRs) > 0 {
		req.APIAllowedCIDRs = &in.APIAllowedCIDRs
	}
	if in.EndpointAccess != "" {
		req.EndpointAccess = pantech.Ptr(in.EndpointAccess)
	}
	return req
}

func (in scaleNodePoolInput) request() pantech.ScaleKubernetesNodePoolRequest {
	return pantech.ScaleKubernetesNodePoolRequest{Workers: in.Workers, AutoscalingEnabled: in.AutoscalingEnabled, MinWorkers: in.MinWorkers, MaxWorkers: in.MaxWorkers}
}

// nodesPrice estimates n nodes of a plan for a month.
func (c *Call) nodesPrice(ctx context.Context, planSlug, placement string, n int) string {
	fallback := fmt.Sprintf("%d nodes, each billed as an instance of plan %s (see list_plans)", n, arg(planSlug))
	cr, err := c.Client.Billing.GetCredit(ctx)
	if err != nil {
		return fallback
	}
	machines := []pantech.PricingEstimateMachineRequest{{PlanSlug: planSlug, Quantity: n, RunningHours: 730, Placement: pantech.Ptr(placement)}}
	est, err := c.Client.Catalog.EstimatePrice(ctx, pantech.PricingEstimateRequest{Currency: cr.Currency, Machines: &machines})
	if err != nil || !est.PricingComplete {
		return fallback
	}
	return fmt.Sprintf("about %s a month for %d nodes", money(est.PricedSubtotalMinor, est.Currency), n)
}

func (c *Call) planByID(ctx context.Context, id, placement string) *pantech.Plan {
	plans, err := c.Client.Catalog.ListPlans(ctx, &pantech.ListPlansParams{Placement: placement})
	if err != nil {
		return nil
	}
	for i := range plans {
		if plans[i].ID == id || plans[i].Slug == id {
			return &plans[i]
		}
	}
	return nil
}
