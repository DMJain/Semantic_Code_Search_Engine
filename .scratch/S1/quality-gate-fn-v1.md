# Quality gate fn-v1 (seed 3)

Gate sample: 200 rows of 6673 eligible (seed 3). Grading rows below: 30.

## 1. k8s.io/kubernetes/pkg/kubelet/network/dns.mergeDNSOptions

input_hash: `f1854ae7866bd422f29343a805720f8ccf59dc995d03d84e9112761a7e6c68d8`

```go
func mergeDNSOptions(existingDNSConfigOptions []string, dnsConfigOptions []v1.PodDNSConfigOption) []string {
	optionsMap := make(map[string]string)
	for _, op := range existingDNSConfigOptions {
		if index := strings.Index(op, ":"); index != -1 {
			optionsMap[op[:index]] = op[index+1:]
		} else {
			optionsMap[op] = ""
		}
	}
	for _, op := range dnsConfigOptions {
		if op.Value != nil {
			optionsMap[op.Name] = *op.Value
		} else {
			optionsMap[op.Name] = ""
		}
	}
	// Reconvert DNS options into a string array.
	options := []string{}
	for opName, opValue := range optionsMap {
		op := opName
		if opValue != "" {
			op = op + ":" + opValue
		}
		options = append(options, op)
	}
	return options
}
```

summary: Merges DNS options from existing and pod configurations, prioritizing pod options if duplicates exist. Uses a map to handle key-value pairs and ensure unique entries.

verdict: 

## 2. k8s.io/kubernetes/pkg/kubelet/kuberuntime.newPodLabels

input_hash: `5530b26ec9fa21cabccec2b2e3c14d1091525434f81b8b9ff258eb7ffa4a5182`

```go
func newPodLabels(pod *v1.Pod) map[string]string {
	labels := map[string]string{}

	// Get labels from v1.Pod
	for k, v := range pod.Labels {
		labels[k] = v
	}

	labels[types.KubernetesPodNameLabel] = pod.Name
	labels[types.KubernetesPodNamespaceLabel] = pod.Namespace
	labels[types.KubernetesPodUIDLabel] = string(pod.UID)

	return labels
}
```

summary: The `newPodLabels` function creates a new map of labels for a Kubernetes pod, combining the pod's existing labels with additional labels for the pod's name, namespace, and UID. This is useful for identifying and organizing pods within a Kubernetes cluster.

verdict: 

## 3. k8s.io/kubernetes/pkg/controller/podgc.(*PodGCController).gcTerminating

input_hash: `4c579dce28b8c402842981fd1f3fbb82580515c6ef2656717a0737ff7fbddfb6`

```go
func (gcc *PodGCController) gcTerminating(ctx context.Context, pods []*v1.Pod) {
	logger := klog.FromContext(ctx)
	logger.V(4).Info("GC'ing terminating pods that are on out-of-service nodes")
	terminatingPods := []*v1.Pod{}
	for _, pod := range pods {
		if isPodTerminating(pod) {
			node, err := gcc.nodeLister.Get(pod.Spec.NodeName)
			if err != nil {
				logger.Error(err, "Failed to get node", "node", klog.KRef("", pod.Spec.NodeName))
				continue
			}
			// Add this pod to terminatingPods list only if the following conditions are met:
			// 1. Node is not ready.
			// 2. Node has `node.kubernetes.io/out-of-service` taint.
			if !nodeutil.IsNodeReady(node) && taints.TaintKeyExists(node.Spec.Taints, v1.TaintNodeOutOfService) {
				logger.V(4).Info("Garbage collecting pod that is terminating", "pod", klog.KObj(pod), "phase", pod.Status.Phase)
				terminatingPods = append(terminatingPods, pod)
			}
		}
	}

	deleteCount := len(terminatingPods)
	if deleteCount == 0 {
		return
	}

	logger.V(4).Info("Garbage collecting pods that are terminating on node tainted with node.kubernetes.io/out-of-service", "numPods", deleteCount)
	// sort only when necessary
	sort.Sort(byEvictionAndCreationTimestamp(terminatingPods))
	var wait sync.WaitGroup
	for i := 0; i < deleteCount; i++ {
		wait.Add(1)
		go func(pod *v1.Pod) {
			defer wait.Done()
			metrics.DeletingPodsTotal.WithLabelValues(pod.Namespace, metrics.PodGCReasonTerminatingOutOfService).Inc()
			if err := gcc.markFailedAndDeletePod(ctx, pod); err != nil {
				// ignore not founds
				utilruntime.HandleErrorWithContext(ctx, err, "Failed to delete terminating pod on out-of-service node", "pod", klog.KObj(pod))
				metrics.DeletingPodsErrorTotal.WithLabelValues(pod.Namespace, metrics.PodGCReasonTerminatingOutOfService).Inc()
			}
		}(terminatingPods[i])
	}
	wait.Wait()
}
```

summary: This function is part of a Kubernetes controller responsible for garbage collecting terminating pods that are on nodes with the `node.kubernetes.io/out-of-service` taint. It filters out-of-service nodes, identifies terminating pods on those nodes, and deletes them asynchronously while tracking metrics and handling errors.

verdict: 

## 4. k8s.io/kubernetes/pkg/scheduler/framework/plugins/dynamicresources.(*DynamicResources).validateDeviceClass

input_hash: `5c9a2b037601a4d2cc83f1a85b3ec8c59ec89bbb2c217c0cce21efd1ba0d3c37`

```go
func (pl *DynamicResources) validateDeviceClass(logger klog.Logger, deviceClassName, requestName string) *fwk.Status {
	if deviceClassName == "" {
		return statusError(logger, fmt.Errorf("request %s: unsupported request type", requestName))
	}

	_, err := pl.draManager.DeviceClasses().Get(deviceClassName)
	if err != nil {
		// If the class cannot be retrieved, allocation cannot proceed.
		if apierrors.IsNotFound(err) {
			// Here we mark the pod as "unschedulable", so it'll sleep in
			// the unscheduleable queue until a DeviceClass event occurs.
			return statusUnschedulable(logger, fmt.Sprintf("request %s: device class %s does not exist", requestName, deviceClassName))
		}
	}
	return nil
}
```

summary: This function validates that a requested device class exists in the system. If the class is not found, it marks the pod as unschedulable until the class becomes available. If the class is valid, it proceeds with the allocation process.

verdict: 

## 5. k8s.io/kubernetes/pkg/scheduler/backend/cache.createUsedPVCRefCounts

input_hash: `bd49b264e5fa661467aab123fbe881c998df2c30779ac1af315f79beee8d4008`

```go
func createUsedPVCRefCounts(nodeInfoMap map[string]*framework.NodeInfo) map[string]int {
	usedPVCRefCounts := make(map[string]int)
	for _, nodeInfo := range nodeInfoMap {
		for pvcKey, count := range nodeInfo.PVCRefCounts {
			usedPVCRefCounts[pvcKey] += count
		}
	}
	return usedPVCRefCounts
}
```

summary: This function calculates the total reference counts for Persistent Volume Claims (PVCs) across all nodes in a Kubernetes cluster. It iterates through each node's PVC reference counts and aggregates them into a single map, which is then returned. This is useful for understanding the overall usage and demand for PVCs in the cluster.

verdict: 

## 6. k8s.io/kubernetes/pkg/kubelet/container/testing.NewMockRuntimeCache

input_hash: `dcf2943cb474d40fcbf19a28c5d30de5679d81018d0edc66651f456169d3eee2`

```go
func NewMockRuntimeCache(t interface {
	mock.TestingT
	Cleanup(func())
}) *MockRuntimeCache {
	mock := &MockRuntimeCache{}
	mock.Mock.Test(t)

	t.Cleanup(func() { mock.AssertExpectations(t) })

	return mock
}
```

summary: `NewMockRuntimeCache` creates a new instance of `MockRuntimeCache`, registering a testing interface and a cleanup function to assert mock expectations. This function is typically used in unit tests to simulate runtime cache behavior.

verdict: 

## 7. k8s.io/kubernetes/pkg/kubelet/kuberuntime.(*containerGC).evictableContainers

input_hash: `88c745d7051e10e63591542589e635ac02a4a143495f75fa1ad27f2000d5e59b`

```go
func (cgc *containerGC) evictableContainers(ctx context.Context, minAge time.Duration) (containersByEvictUnit, error) {
	containers, err := cgc.manager.getContainers(ctx, listOptions{})
	if err != nil {
		return containersByEvictUnit{}, err
	}

	evictUnits := make(containersByEvictUnit)
	newestGCTime := time.Now().Add(-minAge)
	for _, container := range containers {
		// Prune out running containers.
		if container.State == runtimeapi.ContainerState_CONTAINER_RUNNING {
			continue
		}

		createdAt := time.Unix(0, container.CreatedAt)
		if newestGCTime.Before(createdAt) {
			continue
		}

		labeledInfo := getContainerInfoFromLabels(ctx, container.Labels)
		containerInfo := containerGCInfo{
			id:         container.Id,
			name:       container.Metadata.Name,
			createTime: createdAt,
			unknown:    container.State == runtimeapi.ContainerState_CONTAINER_UNKNOWN,
		}
		key := evictUnit{
			uid:  labeledInfo.PodUID,
			name: containerInfo.name,
		}
		evictUnits[key] = append(evictUnits[key], containerInfo)
	}

	return evictUnits, nil
}
```

summary: The `evictableContainers` function in Kubernetes' container garbage collection (GC) subsystem identifies and returns a map of containers that are eligible for eviction. These containers are those that are not currently running and have been created more than a specified minimum age. This function is crucial for managing resources efficiently by removing old, unused containers that are no longer needed.

verdict: 

## 8. k8s.io/kubernetes/pkg/kubelet/server/stats/testing.(*MockProvider_GetPodByCgroupfs_Call).RunAndReturn

input_hash: `fb15fbf0700e2da205aa4d9622a85522a79a6d8e2260b10cfaa76d0f1c3bffd4`

```go
func (_c *MockProvider_GetPodByCgroupfs_Call) RunAndReturn(run func(cgroupfs string) (*v1.Pod, bool)) *MockProvider_GetPodByCgroupfs_Call {
	_c.Call.Return(run)
	return _c
}
```

summary: This function sets up a mock call for the `GetPodByCgroupfs` method, allowing the caller to specify a custom function that determines the pod based on the cgroupfs path. It returns the mock call object for further configuration or execution.

verdict: 

## 9. k8s.io/kubernetes/pkg/kubelet/cm/cpumanager/topology.(CPUDetails).Cores

input_hash: `8bf70d16cc998b6dad7d248e81fae699cb567bd2fd34a046775f26a1436fa865`

```go
func (d CPUDetails) Cores() cpuset.CPUSet {
	var coreIDs []int
	for _, info := range d {
		coreIDs = append(coreIDs, info.CoreID)
	}
	return cpuset.New(coreIDs...)
}
```

summary: The `Cores` method in the `CPUDetails` struct returns a `cpuset.CPUSet` containing all the core IDs associated with the CPUs listed in the `CPUDetails`. This is useful for managing and manipulating CPU resources in a Kubernetes environment, ensuring that workloads are scheduled on the correct cores.

verdict: 

## 10. k8s.io/kubernetes/pkg/kubelet/pod.(*basicManager).GetPodByFullName

input_hash: `7a863848f8cc571760e89cdb7eb9bc3ac50c80272a71cae378e3de2842002076`

```go
func (pm *basicManager) GetPodByFullName(podFullName string) (*v1.Pod, bool) {
	pm.lock.RLock()
	defer pm.lock.RUnlock()
	pod, ok := pm.podByFullName[podFullName]
	return pod, ok
}
```

summary: Retrieves a pod by its full name, returning the pod and a boolean indicating if it was found. Uses a read lock to safely access the pod map.

verdict: 

## 11. k8s.io/kubernetes/pkg/kubelet/cm/dra/plugin.(*DRAPluginManager).Stop

input_hash: `7b4b3e25214651006bc908342b40fbc3a8a3bffd340f0db2741d36094418f266`

```go
func (pm *DRAPluginManager) Stop() {
	defer pm.wg.Wait() // Must run after unlocking our mutex.
	pm.mutex.Lock()
	defer pm.mutex.Unlock()

	logger := klog.FromContext(pm.backgroundCtx)
	pm.cancel(errors.New("Stop was called"))

	// Close all connections, otherwise gRPC keeps doing things in the background.
	// Also cancel all pending wiping.
	for driverName, plugins := range pm.store {
		workerArg := timedworkers.NewWorkArgs(driverName, "")
		pm.pendingWipes.CancelWork(logger, workerArg.KeyFromWorkArgs())
		for _, plugin := range plugins {
			if err := plugin.conn.Close(); err != nil {
				logger.Error(err, "Closing gRPC connection", "driverName", plugin.driverName, "endpoint", plugin.endpoint)
			}
		}
	}
}
```

summary: The `Stop` method in the `DRAPluginManager` struct cancels any remaining background activities and blocks until all goroutines have stopped. It ensures that all gRPC connections are closed and cancels any pending wiping operations. This method is crucial for properly shutting down the plugin manager and releasing resources.

verdict: 

## 12. k8s.io/kubernetes/pkg/kubelet/volumemanager/cache.(volumeNotAttachedError).Error

input_hash: `1195d5e92f4031a5ee2eb4be9bfca26d9d804a922802e8a53043931b131bffbb`

```go
func (err volumeNotAttachedError) Error() string {
	return fmt.Sprintf(
		"volumeName %q does not exist in the list of attached volumes",
		err.volumeName)
}
```

summary: This function implements the `Error` method for a custom error type `volumeNotAttachedError`. It returns a formatted string indicating that a specified volume name is not found in the list of attached volumes. This is useful for error handling in Kubernetes when attempting to access a volume that hasn't been attached to a pod.

verdict: 

## 13. k8s.io/kubernetes/pkg/scheduler/framework/runtime.(*waitingPodsMap).add

input_hash: `98228e7f01c856aa00ebd3fcfa7ee3d16c82200d119bc6363a4124e12b450ffe`

```go
func (m *waitingPodsMap) add(wp *waitingPod) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.pods[wp.GetPod().UID] = wp
}
```

summary: The `add` method adds a new `WaitingPod` to the `waitingPodsMap`. It locks the map, retrieves the pod's UID, and stores the `WaitingPod` in the map under that UID. This ensures thread safety when adding new entries to the map.

verdict: 

## 14. k8s.io/kubernetes/pkg/kubelet/cm/memorymanager.(*staticPolicy).GetPodTopologyHints

input_hash: `5f3a5372336963d6b7eb5bb2fda4fd33443dd95fa7b496e022ec7fff73c1e347`

```go
func (p *staticPolicy) GetPodTopologyHints(logger klog.Logger, s state.State, pod *v1.Pod, operation lifecycle.Operation) map[string][]topologymanager.TopologyHint {
	logger = klog.LoggerWithValues(logger, "pod", klog.KObj(pod), "operation", operation)
	if operation != lifecycle.AddOperation {
		logger.V(2).Info("GetPodTopologyHints skipped, memory manager with static policy supports only add operation")
		return nil
	}

	if v1qos.GetPodQOS(pod) != v1.PodQOSGuaranteed {
		return nil
	}

	// Get a count of how much memory has been requested by Pod.
	reqRsrcs, err := getPodRequestedResources(logger, pod)
	if err != nil {
		logger.Error(err, "Failed to get pod requested resources", "podUID", pod.UID)
		return nil
	}

	// If the pod has pod-level resources but the feature gate is disabled,
	// log it and return nil hints to admit the pod without alignment.
	if (!utilfeature.DefaultFeatureGate.Enabled(features.PodLevelResourceManagers) || !utilfeature.DefaultFeatureGate.Enabled(features.PodLevelResources)) && resourcehelper.IsPodLevelResourcesSet(pod) {
		logger.V(3).Info("Memory Manager pod hint generation skipped, pod is using pod-level resources but the PodLevelResourceManagers feature gate is not enabled", "podUID", pod.UID)
		return nil
	}

	// Validate that if a pod has containers that will be placed in a shared pool,
	// there are actually memory left over for that pool after accounting for all
	// exclusive allocations. If the sum of exclusive memory requests consumes the
	// entire pod-level memory budget, no hints will be generated, causing the pod
	// to be rejected by the Topology Manager.
	if utilfeature.DefaultFeatureGate.Enabled(features.PodLevelResourceManagers) && resourcehelper.IsPodLevelResourcesSet(pod) {
		if err := p.validatePodScopeResources(logger, pod); err != nil {
			logger.V(2).Info("Pod admission will fail", "error", err)
			hints := make(map[string][]topologymanager.TopologyHint)
			for resourceName := range reqRsrcs {
				hints[string(resourceName)] = []topologymanager.TopologyHint{}
			}
			return hints
		}
	}

	if len(reqRsrcs) == 0 {
		return nil
	}

	for _, ctn := range append(pod.Spec.InitContainers, pod.Spec.Containers...) {
		containerBlocks := s.GetMemoryBlocks(string(pod.UID), ctn.Name)
		// Short circuit to regenerate the same hints if there are already
		// memory allocated for the container. This might happen after a
		// kubelet restart, for example.
		if containerBlocks != nil {
			return regenerateHints(logger, pod, &ctn, containerBlocks, reqRsrcs)
		}
	}

	// the pod topology hints calculated only once for all containers, so no need to pass re-usable state
	return p.calculateHints(s.GetMachineState(), pod, reqRsrcs)
}
```

summary: This function calculates topology hints for a pod using a static policy. It filters out pods that don't meet certain criteria (like QoS class or feature gate settings) and generates hints based on the pod's requested resources and the machine's memory blocks. The hints are used by the Topology Manager to make decisions about resource allocation.

verdict: 

## 15. k8s.io/kubernetes/pkg/controller/storageversionmigrator.(*MigrationRunnerController).Run

input_hash: `837f1b2f30e0f1d09ab4056dc02271aa9cb93a15a639cda3bd980ae158a8a4d4`

```go
func (crc *MigrationRunnerController) Run(ctx context.Context) {
	defer utilruntime.HandleCrash()

	logger := klog.FromContext(ctx)
	logger.Info("Starting", "controller", MigrationRunnerControllerName)

	var wg sync.WaitGroup
	defer func() {
		logger.Info("Shutting down", "controller", MigrationRunnerControllerName)
		crc.queue.ShutDown()
		wg.Wait()
	}()

	if !cache.WaitForNamedCacheSyncWithContext(ctx, crc.svmSynced) {
		return
	}

	wg.Go(func() {
		wait.UntilWithContext(ctx, crc.worker, time.Second)
	})
	<-ctx.Done()
}
```

summary: This function runs a controller for managing migrations, handling shutdown gracefully and ensuring all resources are properly released. It waits for necessary caches to sync before starting the worker loop and shuts down cleanly when the context is canceled.

verdict: 

## 16. k8s.io/kubernetes/pkg/controller.(*UIDTrackingControllerExpectations).DeletionObserved

input_hash: `19161b7fe1716f817a306e8143ed2471a0bb77755131dd8d40cb95867303ffde`

```go
func (u *UIDTrackingControllerExpectations) DeletionObserved(logger klog.Logger, rcKey, deleteKey string) {
	u.uidStoreLock.Lock()
	defer u.uidStoreLock.Unlock()

	uids := u.GetUIDs(rcKey)
	if uids != nil && uids.Has(deleteKey) {
		logger.V(4).Info("Controller received delete for pod", "controller", rcKey, "key", deleteKey)
		u.ControllerExpectationsInterface.DeletionObserved(logger, rcKey)
		uids.Delete(deleteKey)
	}
}
```

summary: This function records a pod deletion expectation for a given controller. It checks if the pod is expected to be deleted and logs the event before updating the expectations store.

verdict: 

## 17. k8s.io/kubernetes/pkg/scheduler/profile.(Map).HandlesSchedulerName

input_hash: `02409a7a0ad4e8d6f2fe4de37d90d640c98190a90813e44f4875c63a65049626`

```go
func (m Map) HandlesSchedulerName(name string) bool {
	_, ok := m[name]
	return ok
}
```

summary: HandlesSchedulerName checks if a profile in the map is configured to handle a specific scheduler name. It returns true if the scheduler name is found in the map, indicating that the profile handles it.

verdict: 

## 18. k8s.io/kubernetes/pkg/controller/serviceaccount.makeServiceAccountKey

input_hash: `a5accf1c33f9ffa05c24f621209538da61182b25662b7dc1a803c83eefa3cc0c`

```go
func makeServiceAccountKey(sa *v1.ServiceAccount) serviceAccountQueueKey {
	return serviceAccountQueueKey{
		namespace: sa.Namespace,
		name:      sa.Name,
		uid:       sa.UID,
	}
}
```

summary: This function generates a unique key for a Kubernetes ServiceAccount by combining its namespace, name, and UID. It's used to identify and queue ServiceAccounts for processing in Kubernetes controllers.

verdict: 

## 19. k8s.io/kubernetes/pkg/controller/podautoscaler.(*HorizontalController).processNextWorkItem

input_hash: `df03afea11f59d6898ec777306b9ea71d8983240e0c48fd730562d3246f78670`

```go
func (a *HorizontalController) processNextWorkItem(ctx context.Context) bool {
	key, quit := a.queue.Get()
	if quit {
		return false
	}
	defer a.queue.Done(key)

	deleted, err := a.reconcileKey(ctx, key)
	if err != nil {
		utilruntime.HandleError(err)
	}
	// Add request processing HPA to queue with resyncPeriod delay.
	// Requests are always added to queue with resyncPeriod delay. If there's already request
	// for the HPA in the queue then a new request is always dropped. Requests spend resyncPeriod
	// in queue so HPAs are processed every resyncPeriod.
	// Request is added here just in case last resync didn't insert request into the queue. This
	// happens quite often because there is race condition between adding request after resyncPeriod
	// and removing them from queue. Request can be added by resync before previous request is
	// removed from queue. If we didn't add request here then in this case one request would be dropped
	// and HPA would process after 2 x resyncPeriod.
	if !deleted {
		a.queue.AddRateLimited(key)
	}

	return true
}
```

summary: This function processes the next work item from a queue in a Kubernetes HorizontalController. It dequeues an item, processes it using `reconcileKey`, and then re-enqueues it with a delay if the processing was successful. This ensures that HorizontalPodAutoscalers are periodically reconciled, even if their last reconciliation was successful.

verdict: 

## 20. k8s.io/kubernetes/pkg/controller/validatingadmissionpolicystatus.NewController

input_hash: `c53d14efcad1ecb96f3e2cb18a6ee8826f2aa884ad92fbb5dc6eb89c9305c83b`

```go
func NewController(policyInformer informerv1.ValidatingAdmissionPolicyInformer, policyClient admissionregistrationv1.ValidatingAdmissionPolicyInterface, typeChecker *validatingadmissionpolicy.TypeChecker) (*Controller, error) {
	c := &Controller{
		policyInformer: policyInformer,
		policyQueue: workqueue.NewTypedRateLimitingQueueWithConfig(
			workqueue.DefaultTypedControllerRateLimiter[string](),
			workqueue.TypedRateLimitingQueueConfig[string]{Name: ControllerName},
		),
		policyClient: policyClient,
		typeChecker:  typeChecker,
	}
	reg, err := policyInformer.Informer().AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) {
			c.enqueuePolicy(obj)
		},
		UpdateFunc: func(oldObj, newObj interface{}) {
			c.enqueuePolicy(newObj)
		},
	})
	if err != nil {
		return nil, err
	}
	c.policySynced = reg.HasSynced
	return c, nil
}
```

summary: This function creates a new controller for managing ValidatingAdmissionPolicies in Kubernetes. It sets up an informer to watch for policy changes and a workqueue to process those changes asynchronously. The controller uses a type checker to validate policy configurations and enqueues policies for processing when they are added or updated.

verdict: 

## 21. k8s.io/kubernetes/pkg/scheduler/backend/queue.(*PriorityQueue).UnschedulablePods

input_hash: `0b7d710ca02a9bdd197783e3ecdabf64838698e2ebbdc35cf403494a6cb12660`

```go
func (p *PriorityQueue) UnschedulablePods() []*v1.Pod {
	p.lock.RLock()
	defer p.lock.RUnlock()
	var result []*v1.Pod
	for _, entity := range p.unschedulableEntities.entityInfoMap {
		entity.ForEachPodInfo(func(pInfo *framework.QueuedPodInfo) bool {
			result = append(result, pInfo.Pod)
			return true
		})
	}
	return result
}
```

summary: The `UnschedulablePods` function returns a list of all pods that are currently in an unschedulable state. This is useful for identifying and managing pods that are temporarily unable to be scheduled onto nodes, allowing for better resource allocation and troubleshooting.

verdict: 

## 22. k8s.io/kubernetes/pkg/controller/testutil.(*FakeNodeHandler).Create

input_hash: `f31eeadad868a7decd1d1a6d6de6a368285aa0087b313772e76d670949b33b11`

```go
func (m *FakeNodeHandler) Create(_ context.Context, node *v1.Node, _ metav1.CreateOptions) (*v1.Node, error) {
	m.lock.Lock()
	defer func() {
		m.RequestCount++
		m.lock.Unlock()
	}()
	for _, n := range m.Existing {
		if n.Name == node.Name {
			return nil, apierrors.NewAlreadyExists(api.Resource("nodes"), node.Name)
		}
	}
	if m.CreateHook == nil || m.CreateHook(m, node) {
		nodeCopy := *node
		m.CreatedNodes = append(m.CreatedNodes, &nodeCopy)
		return node, nil
	}
	return nil, errors.New("create error")
}
```

summary: This function simulates creating a new Node in a Kubernetes cluster. It checks if a Node with the same name already exists, and if not, it adds the new Node to a fake store. If a Node with the same name exists, it returns an error indicating that the Node already exists. The function also allows for a custom hook to be called before creating the Node.

verdict: 

## 23. k8s.io/kubernetes/pkg/controller/volume/persistentvolume/metrics.(*pvAndPVCCountCollector).pvcCollect

input_hash: `5b90585ca6dd39c39480b8bd149419433a0d67d96d069f76ce025ddd0f96d14d`

```go
func (collector *pvAndPVCCountCollector) pvcCollect(ch chan<- metrics.Metric) {
	boundNumber := make(map[pvcBindingMetricDimensions]int)
	unboundNumber := make(map[pvcBindingMetricDimensions]int)
	for _, pvcObj := range collector.pvcLister.List() {
		pvc, ok := pvcObj.(*v1.PersistentVolumeClaim)
		if !ok {
			continue
		}
		if pvc.Status.Phase == v1.ClaimBound {
			boundNumber[getPVCMetricDimensions(pvc)]++
		} else {
			unboundNumber[getPVCMetricDimensions(pvc)]++
		}
	}
	for pvcLabels, number := range boundNumber {
		ch <- metrics.NewLazyConstMetric(
			boundPVCCountDesc,
			metrics.GaugeValue,
			float64(number),
			pvcLabels.namespace, pvcLabels.storageClassName, pvcLabels.volumeAttributesClassName)
	}
	for pvcLabels, number := range unboundNumber {
		ch <- metrics.NewLazyConstMetric(
			unboundPVCCountDesc,
			metrics.GaugeValue,
			float64(number),
			pvcLabels.namespace, pvcLabels.storageClassName, pvcLabels.volumeAttributesClassName)
	}
}
```

summary: This function collects metrics for Persistent Volume Claims (PVCs) in a Kubernetes cluster, counting how many are bound and unbound. It uses a lister to retrieve PVC objects, categorizes them based on their phase, and then sends metrics to a channel for further processing. This helps in monitoring the state of PVCs and understanding resource allocation in the cluster.

verdict: 

## 24. k8s.io/kubernetes/pkg/scheduler.NewHTTPExtender

input_hash: `8903c556de73d1be9d48f919052b539265466e84330376c36df59fd2e3b4fa8d`

```go
func NewHTTPExtender(config *schedulerapi.Extender) (fwk.Extender, error) {
	if config.HTTPTimeout.Duration.Nanoseconds() == 0 {
		config.HTTPTimeout.Duration = time.Duration(DefaultExtenderTimeout)
	}

	transport, err := makeTransport(config)
	if err != nil {
		return nil, err
	}
	client := &http.Client{
		Transport: transport,
		Timeout:   config.HTTPTimeout.Duration,
	}
	managedResources := sets.New[string]()
	for _, r := range config.ManagedResources {
		managedResources.Insert(string(r.Name))
	}
	return &HTTPExtender{
		extenderURL:      config.URLPrefix,
		preemptVerb:      config.PreemptVerb,
		filterVerb:       config.FilterVerb,
		prioritizeVerb:   config.PrioritizeVerb,
		bindVerb:         config.BindVerb,
		weight:           config.Weight,
		client:           client,
		nodeCacheCapable: config.NodeCacheCapable,
		managedResources: managedResources,
		ignorable:        config.Ignorable,
	}, nil
}
```

summary: `NewHTTPExtender` creates an HTTP-based extender for Kubernetes scheduling, initializing it with configuration settings such as timeout durations and managed resources. This function is crucial for integrating custom scheduling logic with Kubernetes, allowing for more flexible and powerful scheduling decisions based on external factors.

verdict: 

## 25. k8s.io/kubernetes/pkg/scheduler/testing.(*CSIDriverWrapper).Name

input_hash: `0c843a2aa9b920e671fbe928c00560a1d7f7691d38576a038637c3340ddfff55`

```go
func (c *CSIDriverWrapper) Name(n string) *CSIDriverWrapper {
	c.SetName(n)
	return c
}
```

summary: The `Name` method sets the name of the inner `CSIDriver` object to the provided string `n`. This is useful for identifying and referencing the CSIDriver within Kubernetes. The method returns a pointer to the `CSIDriverWrapper` instance, allowing for method chaining.

verdict: 

## 26. k8s.io/kubernetes/pkg/kubelet/config.isAnnotationMapEqual

input_hash: `f284999f14831e443905db6e6372f7a61224601e1b07059fd56b77d8c0034097`

```go
func isAnnotationMapEqual(existingMap, candidateMap map[string]string) bool {
	if candidateMap == nil {
		candidateMap = make(map[string]string)
	}
	for k, v := range candidateMap {
		if isLocalAnnotationKey(k) {
			continue
		}
		if existingValue, ok := existingMap[k]; ok && existingValue == v {
			continue
		}
		return false
	}
	for k := range existingMap {
		if isLocalAnnotationKey(k) {
			continue
		}
		// stale entry in existing map.
		if _, exists := candidateMap[k]; !exists {
			return false
		}
	}
	return true
}
```

summary: isAnnotationMapEqual checks if two annotation maps are equal, ignoring local annotations. It returns true if all non-local annotations in the candidate map match those in the existing map, and vice versa. This function is used to determine if updates to annotations are necessary.

verdict: 

## 27. k8s.io/kubernetes/pkg/controller/endpointslicemirroring/metrics.(*Cache).DeleteEndpoints

input_hash: `2ec93e5d3faf0d5b80b5e707cfa96a93bf88a3dd692af097c0b462dfcf203929`

```go
func (c *Cache) DeleteEndpoints(endpointsNN types.NamespacedName) {
	c.lock.Lock()
	defer c.lock.Unlock()

	if spCache, ok := c.cache[endpointsNN]; ok {
		c.numEndpoints = c.numEndpoints - spCache.numEndpoints()
		delete(c.cache, endpointsNN)
		c.updateMetrics()
	}
}
```

summary: The `DeleteEndpoints` function removes references to an Endpoints resource from the global cache and updates the corresponding metrics. It ensures thread safety by locking and unlocking the cache during the operation.

verdict: 

## 28. k8s.io/kubernetes/pkg/scheduler/framework/runtime.(*podsInPreBindMap).add

input_hash: `3f793ddd0ef34af1fbaa20c186271dcac3bcaff0f2104158ef13a8ae0d5836ca`

```go
func (pbm *podsInPreBindMap) add(uid types.UID, cancel context.CancelCauseFunc) {
	pbm.mu.Lock()
	defer pbm.mu.Unlock()
	pbm.pods[uid] = &podInPreBind{cancel: cancel}
}
```

summary: The `add` method adds a pod to a map, overwriting any existing entry with the same UID. It uses a mutex to ensure thread safety during the operation.

verdict: 

## 29. k8s.io/kubernetes/pkg/scheduler.(*Scheduler).addPod

input_hash: `6a955a15b23755a85cf802a57acf0e462cc01ddc7eb7c4a069a867b956ea29a5`

```go
func (sched *Scheduler) addPod(obj interface{}) {
	logger := sched.logger
	pod, ok := obj.(*v1.Pod)
	if !ok {
		utilruntime.HandleErrorWithLogger(logger, nil, "Cannot convert to *v1.Pod", "obj", obj)
		return
	}

	isPodDeferred := utilfeature.DefaultFeatureGate.Enabled(features.InPlacePodVerticalScalingSchedulerPreemption) && resource.IsPodResizeDeferred(pod)
	if responsibleForPod(pod, sched.Profiles) && (!assignedPod(pod) || isPodDeferred) {
		sched.addPodToSchedulingQueue(pod)
	}
	if assignedPod(pod) {
		sched.addAssignedPodToCache(pod)
	}
}
```

summary: This function adds a pod to the scheduler's queue and cache if it meets certain conditions, such as being responsible for the pod and not already being assigned. It handles errors and checks feature flags to determine if the pod should be deferred.

verdict: 

## 30. k8s.io/kubernetes/pkg/scheduler/framework/plugins/gangscheduling.(*GangScheduling).areSameHierarchy

input_hash: `ba9751439c14976c460f3035b013c703678d4a8f1a4ed13511c5814ea294676c`

```go
func (pl *GangScheduling) areSameHierarchy(logger klog.Logger, namespace, podGroupName string, targetKey fwk.EntityKey) bool {
	if !pl.isCompositePodGroupEnabled {
		return fwk.PodGroupKey(namespace, podGroupName) == targetKey
	}

	podGroupKey := fwk.PodGroupKey(namespace, podGroupName)
	root1, ok1, err := pl.podGroupManager.GetRootKeyForGroup(podGroupKey)
	if err != nil {
		utilruntime.HandleErrorWithLogger(logger, err, "Failed to get root key for key", "key", podGroupKey)
		return false
	}
	if !ok1 {
		return false
	}
	root2, ok2, err := pl.podGroupManager.GetRootKeyForGroup(targetKey)
	if err != nil {
		utilruntime.HandleErrorWithLogger(logger, err, "Failed to get root key for key", "key", targetKey)
		return false
	}
	if !ok2 {
		return false
	}
	return root1 == root2
}
```

summary: This function checks if a given pod group is in the same hierarchy as a target key by retrieving the root composite pod group for both keys and comparing them. It handles errors and returns true if the root keys match, indicating the pod groups are in the same hierarchy.

verdict: 

