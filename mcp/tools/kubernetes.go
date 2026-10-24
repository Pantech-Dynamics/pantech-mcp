package tools

import (
	"context"
	"fmt"
	"slices"

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

type deleteClusterInput struct {
	ClusterID string `json:"cluster_id" jsonschema:"The Kubernetes cluster's id."`
	Confirmation
}

type createNodePoolInput struct {
	ClusterID          string            `json:"cluster_id" jsonschema:"The cluster's id."`
	Name               string            `json:"name" jsonschema:"The pool's name, unique in the cluster."`
	PlanSlug           string            `json:"plan_slug" jsonschema:"The plan every worker of the pool uses, from list_plans."`
	Workers            int               `json:"workers,omitempty" jsonschema:"Worker nodes, 1 to 10 (default 1)."`
	AutoscalingEnabled bool              `json:"autoscaling_enabled,omitempty" jsonschema:"Let the platform add and remove workers between min_workers and max_workers."`
	MinWorkers         int               `json:"min_workers,omitempty" jsonschema:"With autoscaling: fewest workers, 1 to 10."`
	MaxWorkers         int               `json:"max_workers,omitempty" jsonschema:"With autoscaling: most workers, min_workers to 10."`
	Labels             map[string]string `json:"labels,omitempty" jsonschema:"Kubernetes labels for the pool's nodes."`
	Confirmation
}

type nodePoolInput struct {
	ClusterID string `json:"cluster_id" jsonschema:"The cluster's id."`
	PoolID    string `json:"pool_id" jsonschema:"The node pool's id, from list_node_pools."`
	Confirmation
}

type toggleAddonInput struct {
	ClusterID string `json:"cluster_id" jsonschema:"The cluster's id."`
	AddonName string `json:"addon_name" jsonschema:"metrics-server, csi-driver or ingress-controller."`
	Enabled   bool   `json:"enabled" jsonschema:"true to turn it on, false to turn it off."`
	Confirmation
}

var addonNames = []string{"metrics-server", "csi-driver", "ingress-controller"}

func init() {
	addConfirmed(Spec{
		Name: "delete_kubernetes_cluster", Title: "Delete a Kubernetes cluster",
		Description: "Delete a Kubernetes cluster. It stops at once and stops billing; its nodes and disks are destroyed 12 hours later and cannot be recovered, and volumes created through persistent volume claims are deleted with it. Needs an AI app connected with Manage and delete access.",
		Risk:        Destructive, Destructive: true, Idempotent: true,
		Routes: []string{"GET /kubernetes-clusters/{cluster_id}", "DELETE /kubernetes-clusters/{cluster_id}", "GET /operations/{operation_id}"},
	}, func(ctx context.Context, c *Call, in deleteClusterInput) (*Preview, error) {
		if err := checkIDs("cluster_id", in.ClusterID); err != nil {
			return nil, err
		}
		cl, err := c.Client.Kubernetes.GetCluster(ctx, in.ClusterID)
		if err != nil {
			return nil, err
		}
		return &Preview{
			Action: fmt.Sprintf("Permanently delete Kubernetes cluster %s (%d nodes). Every workload stops at once; its nodes and disks are destroyed 12 hours later and cannot be recovered.", cl.ID, cl.Nodes),
			Warnings: []string{
				"Data loss: volumes your workloads created through persistent volume claims are deleted with the cluster.",
				deleteAccessNote,
			},
			Data: map[string]any{"cluster": cl},
		}, nil
	}, func(ctx context.Context, c *Call, in deleteClusterInput) (*Result, error) {
		acc, err := c.Client.Kubernetes.DeleteCluster(ctx, in.ClusterID, c.writeOpts()...)
		if err != nil {
			return nil, err
		}
		return c.finish(ctx, acc, "Kubernetes cluster "+in.ClusterID+" deleted")
	})

	clusterAction := func(name, title, desc, verb, route string, call func(*pantech.KubernetesService) func(context.Context, string, ...pantech.RequestOption) (*pantech.Accepted, error)) {
		add(Spec{
			Name: name, Title: title, Description: desc,
			Risk: Write, Idempotent: true,
			Routes: []string{route, "GET /operations/{operation_id}"},
		}, func(ctx context.Context, c *Call, in clusterInput) (*Result, error) {
			if err := checkIDs("cluster_id", in.ClusterID); err != nil {
				return nil, err
			}
			acc, err := call(c.Client.Kubernetes)(ctx, in.ClusterID, c.writeOpts()...)
			if err != nil {
				return nil, err
			}
			return c.finish(ctx, acc, fmt.Sprintf("Kubernetes cluster %s %s", in.ClusterID, verb))
		})
	}
	clusterAction("start_kubernetes_cluster", "Start a Kubernetes cluster", "Start a stopped Kubernetes cluster; its nodes are billed for compute again once it runs. Waits up to a minute.", "started",
		"POST /kubernetes-clusters/{cluster_id}/start", func(s *pantech.KubernetesService) func(context.Context, string, ...pantech.RequestOption) (*pantech.Accepted, error) {
			return s.StartCluster
		})
	clusterAction("stop_kubernetes_cluster", "Stop a Kubernetes cluster", "Stop a running Kubernetes cluster; its workloads stop and only its disks are billed while stopped. Waits up to a minute.", "stopped",
		"POST /kubernetes-clusters/{cluster_id}/stop", func(s *pantech.KubernetesService) func(context.Context, string, ...pantech.RequestOption) (*pantech.Accepted, error) {
			return s.StopCluster
		})

	add(Spec{
		Name: "list_node_pools", Title: "List node pools",
		Description: "List a Kubernetes cluster's node pools: plan, worker counts, autoscaling, labels and taints. A pool id is what scale_node_pool and delete_node_pool take.",
		Risk:        Read, Idempotent: true,
		Routes: []string{"GET /kubernetes-clusters/{cluster_id}/node-pools"},
	}, func(ctx context.Context, c *Call, in clusterInput) (*Result, error) {
		if err := checkIDs("cluster_id", in.ClusterID); err != nil {
			return nil, err
		}
		pools, err := c.Client.Kubernetes.ListNodePools(ctx, in.ClusterID)
		if err != nil {
			return nil, err
		}
		return ok(plural(len(pools), "node pool", "node pools")+" in cluster "+in.ClusterID+".", pools)
	})

	addConfirmed(Spec{
		Name: "create_node_pool", Title: "Add a node pool",
		Description: "Add a node pool to a Kubernetes cluster: workers of their own plan, labelled pantech.io/node-pool=<name>. Each worker is billed as an instance of the plan once it joins.",
		Risk:        Cost,
		Routes: []string{"GET /kubernetes-clusters/{cluster_id}", "GET /plans", "GET /billing/credit", "POST /pricing/estimate",
			"POST /kubernetes-clusters/{cluster_id}/node-pools", "GET /operations/{operation_id}"},
	}, func(ctx context.Context, c *Call, in createNodePoolInput) (*Preview, error) {
		if err := checkIDs("cluster_id", in.ClusterID); err != nil {
			return nil, err
		}
		cl, err := c.Client.Kubernetes.GetCluster(ctx, in.ClusterID)
		if err != nil {
			return nil, err
		}
		workers := in.Workers
		if workers == 0 {
			workers = 1
		}
		placement := placementOf(str(cl.SubnetID), "")
		p := &Preview{
			Action: fmt.Sprintf("Add a node pool of %d workers on plan %s to cluster %s.", workers, arg(in.PlanSlug), cl.ID),
			Price:  c.nodesPrice(ctx, in.PlanSlug, placement, workers),
			Data:   map[string]any{"cluster": cl, "request": in.request()},
		}
		if in.AutoscalingEnabled {
			p.Warnings = append(p.Warnings, fmt.Sprintf("Autoscaling adds and removes billed workers between %d and %d.", in.MinWorkers, in.MaxWorkers))
		}
		return p, nil
	}, func(ctx context.Context, c *Call, in createNodePoolInput) (*Result, error) {
		acc, err := c.Client.Kubernetes.CreateNodePool(ctx, in.ClusterID, in.request(), c.writeOpts()...)
		if err != nil {
			return nil, err
		}
		return c.finish(ctx, acc, "node pool "+acc.ResourceID+" added to cluster "+in.ClusterID)
	})

	addConfirmed(Spec{
		Name: "delete_node_pool", Title: "Delete a node pool",
		Description: "Remove a node pool: its workers leave the cluster (pods move to other nodes that can run them) and are destroyed with their local data. Needs an AI app connected with Manage and delete access.",
		Risk:        Destructive, Destructive: true, Idempotent: true,
		Routes: []string{"GET /kubernetes-clusters/{cluster_id}/node-pools", "DELETE /kubernetes-clusters/{cluster_id}/node-pools/{pool_id}", "GET /operations/{operation_id}"},
	}, func(ctx context.Context, c *Call, in nodePoolInput) (*Preview, error) {
		if err := checkIDs("cluster_id", in.ClusterID, "pool_id", in.PoolID); err != nil {
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
			return nil, errorf("Cluster %s has no node pool %s. Call list_node_pools for its pools.", in.ClusterID, in.PoolID)
		}
		return &Preview{
			Action: fmt.Sprintf("Delete node pool %s of cluster %s and destroy its %s.", pool.ID, in.ClusterID, plural(pool.Workers, "worker", "workers")),
			Warnings: []string{
				"Data loss: anything stored on those nodes' own disks is destroyed. Pods move to other nodes only if some can run them; otherwise they stay pending.",
				deleteAccessNote,
			},
			Data: map[string]any{"node_pool": pool},
		}, nil
	}, func(ctx context.Context, c *Call, in nodePoolInput) (*Result, error) {
		acc, err := c.Client.Kubernetes.DeleteNodePool(ctx, in.ClusterID, in.PoolID, c.writeOpts()...)
		if err != nil {
			return nil, err
		}
		return c.finish(ctx, acc, "node pool "+in.PoolID+" deleted")
	})

	add(Spec{
		Name: "list_kubernetes_addons", Title: "List Kubernetes add-ons",
		Description: "List a cluster's managed add-ons (metrics-server, csi-driver, ingress-controller): enabled or not, version and status.",
		Risk:        Read, Idempotent: true,
		Routes: []string{"GET /kubernetes-clusters/{cluster_id}/addons"},
	}, func(ctx context.Context, c *Call, in clusterInput) (*Result, error) {
		if err := checkIDs("cluster_id", in.ClusterID); err != nil {
			return nil, err
		}
		addons, err := c.Client.Kubernetes.ListAddons(ctx, in.ClusterID)
		if err != nil {
			return nil, err
		}
		return ok(plural(len(addons), "add-on", "add-ons")+" on cluster "+in.ClusterID+".", addons)
	})

	addConfirmed(Spec{
		Name: "toggle_kubernetes_addon", Title: "Turn a Kubernetes add-on on or off",
		Description: "Turn a managed add-on on or off: metrics-server, csi-driver or ingress-controller. The platform keeps it that way (a change made with kubectl is put back). Turning one off removes what it provides from running workloads.",
		Risk:        Destructive, Destructive: true, Idempotent: true,
		Routes: []string{"GET /kubernetes-clusters/{cluster_id}/addons", "POST /kubernetes-clusters/{cluster_id}/addons/{addon_name}/toggle", "GET /operations/{operation_id}"},
	}, func(ctx context.Context, c *Call, in toggleAddonInput) (*Preview, error) {
		if err := checkIDs("cluster_id", in.ClusterID); err != nil {
			return nil, err
		}
		if !slices.Contains(addonNames, in.AddonName) {
			return nil, errorf("addon_name must be metrics-server, csi-driver or ingress-controller.")
		}
		p := &Preview{Data: map[string]any{"addon_name": in.AddonName, "enabled": in.Enabled}}
		if addons, err := c.Client.Kubernetes.ListAddons(ctx, in.ClusterID); err == nil {
			for _, a := range addons {
				if a.Name == in.AddonName {
					p.Data = map[string]any{"current": a, "enabled": in.Enabled}
				}
			}
		}
		if in.Enabled {
			p.Action = fmt.Sprintf("Turn on the %s add-on of cluster %s.", in.AddonName, in.ClusterID)
			if in.AddonName == "ingress-controller" {
				p.Warnings = append(p.Warnings, "The ingress controller can route traffic from the internet to services that ingress rules expose.")
			}
		} else {
			p.Action = fmt.Sprintf("Turn off the %s add-on of cluster %s.", in.AddonName, in.ClusterID)
			p.Warnings = append(p.Warnings, map[string]string{
				"metrics-server":     "Autoscaling and kubectl top stop working without metrics.",
				"csi-driver":         "Workloads can no longer get new persistent volumes, and mounting existing ones may fail.",
				"ingress-controller": "Traffic routed through ingress rules stops reaching your services.",
			}[in.AddonName])
		}
		return p, nil
	}, func(ctx context.Context, c *Call, in toggleAddonInput) (*Result, error) {
		acc, err := c.Client.Kubernetes.ToggleAddon(ctx, in.ClusterID, in.AddonName, pantech.ToggleKubernetesAddonRequest{Enabled: in.Enabled}, c.writeOpts()...)
		if err != nil {
			return nil, err
		}
		state := "off"
		if in.Enabled {
			state = "on"
		}
		return c.finish(ctx, acc, fmt.Sprintf("add-on %s of cluster %s turned %s", in.AddonName, in.ClusterID, state))
	})

	add(Spec{
		Name: "list_cluster_nodes", Title: "List a cluster's nodes",
		Description: "List a running Kubernetes cluster's nodes as Kubernetes reports them now: role, Ready or NotReady, internal address, capacity and kubelet version.",
		Risk:        Read, Idempotent: true,
		Routes: []string{"GET /kubernetes-clusters/{cluster_id}/nodes"},
	}, func(ctx context.Context, c *Call, in clusterInput) (*Result, error) {
		if err := checkIDs("cluster_id", in.ClusterID); err != nil {
			return nil, err
		}
		nodes, err := c.Client.Kubernetes.ListNodes(ctx, in.ClusterID)
		if err != nil {
			return nil, err
		}
		ready := 0
		for _, n := range nodes.Nodes {
			if n.Status == "Ready" {
				ready++
			}
		}
		return ok(fmt.Sprintf("%s in cluster %s, %d Ready.", plural(len(nodes.Nodes), "node", "nodes"), in.ClusterID, ready), nodes)
	})

	add(Spec{
		Name: "list_cluster_workloads", Title: "List a cluster's workloads",
		Description: "List every Service and pod in a running Kubernetes cluster, in every namespace, read live: pod status, readiness and restarts, and the public URL of exposed services. Never returns manifests, secrets or a kubeconfig.",
		Risk:        Read, Idempotent: true,
		Routes: []string{"GET /kubernetes-clusters/{cluster_id}/workloads"},
	}, func(ctx context.Context, c *Call, in clusterInput) (*Result, error) {
		if err := checkIDs("cluster_id", in.ClusterID); err != nil {
			return nil, err
		}
		w, err := c.Client.Kubernetes.GetWorkloads(ctx, in.ClusterID)
		if err != nil {
			return nil, err
		}
		return ok(fmt.Sprintf("Cluster %s runs %s and %s.", in.ClusterID, plural(len(w.Services), "service", "services"), plural(len(w.Pods), "pod", "pods")), w)
	})
}

func (in createNodePoolInput) request() pantech.CreateKubernetesNodePoolRequest {
	req := pantech.CreateKubernetesNodePoolRequest{Name: in.Name, PlanID: in.PlanSlug}
	if in.Workers > 0 {
		req.Workers = pantech.Ptr(in.Workers)
	}
	if in.AutoscalingEnabled {
		req.AutoscalingEnabled = pantech.Ptr(true)
	}
	if in.MinWorkers > 0 {
		req.MinWorkers = pantech.Ptr(in.MinWorkers)
	}
	if in.MaxWorkers > 0 {
		req.MaxWorkers = pantech.Ptr(in.MaxWorkers)
	}
	if len(in.Labels) > 0 {
		req.Labels = &in.Labels
	}
	return req
}
