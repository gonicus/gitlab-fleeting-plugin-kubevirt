package kubevirt

import (
	"context"
	"fmt"
	"path"
	"strconv"

	"github.com/hashicorp/go-hclog"
	"gitlab.com/gitlab-org/fleeting/fleeting/provider"
	k8scorev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	k8smetav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	v1 "kubevirt.io/api/core/v1"
	"kubevirt.io/client-go/kubecli"
)

var _ provider.InstanceGroup = (*InstanceGroup)(nil)

type InstanceGroup struct {
	UseInClusterConfig     bool   `json:"useInClusterConfig"`
	Kubeconfig             string `json:"kubeconfig"`
	VmLabelSelectorKey     string `json:"vmLabelKey"`
	VmLabelSelectorValue   string `json:"vmLabelValue"`
	VmNamespace            string `json:"vmNamespace"`
	VmNamePrefix           string `json:"vmNamePrefix"`
	VmRAM                  string `json:"vmRAM"`
	VmCPUCores             string `json:"vmCPUCores"`
	VmCloudInitUserData    string `json:"vmCloudInitUserData"`
	VmRunnerImage          string `json:"vmRunnerImage"`
	VmReadinessProbeScript string `json:"vmReadinessProbeScript"`

	client kubecli.KubevirtClient

	log hclog.Logger

	settings provider.Settings
}

// Init implements provider.InstanceGroup
func (g *InstanceGroup) Init(ctx context.Context, logger hclog.Logger, settings provider.Settings) (provider.ProviderInfo, error) {
	var contextName string
	if g.UseInClusterConfig {
		restConfig, err := rest.InClusterConfig()
		if err != nil {
			return provider.ProviderInfo{}, fmt.Errorf("failed getting in-cluster config: %w", err)
		}
		g.client, err = kubecli.GetKubevirtClientFromRESTConfig(restConfig)
		if err != nil {
			return provider.ProviderInfo{}, fmt.Errorf("failed creating kubevirt client: %w", err)
		}
	} else {
		clientConfig, err := clientcmd.NewClientConfigFromBytes([]byte(g.Kubeconfig))
		if err != nil {
			return provider.ProviderInfo{}, fmt.Errorf("failed parsing kubeconfig: %w", err)
		}
		g.client, err = kubecli.GetKubevirtClientFromClientConfig(clientConfig)
		if err != nil {
			return provider.ProviderInfo{}, fmt.Errorf("failed creating kubevirt client: %w", err)
		}
		rawConfig, err := clientConfig.MergedRawConfig()
		if err != nil {
			return provider.ProviderInfo{}, fmt.Errorf("failed merging raw config: %w", err)
		}
		contextName = rawConfig.CurrentContext
	}

	if g.VmLabelSelectorKey == "" ||
		g.VmLabelSelectorValue == "" ||
		g.VmNamespace == "" ||
		g.VmNamePrefix == "" ||
		g.VmRAM == "" ||
		g.VmCPUCores == "" ||
		g.VmCloudInitUserData == "" ||
		g.VmRunnerImage == "" ||
		g.VmReadinessProbeScript == "" {
		return provider.ProviderInfo{}, fmt.Errorf("missing required parameter")
	}

	g.settings = settings
	g.log = logger

	return provider.ProviderInfo{
		ID:        path.Join("kubevirt", contextName, g.VmNamespace),
		MaxSize:   1000,
		Version:   Version.String(),
		BuildInfo: Version.BuildInfo(),
	}, nil
}

// ConnectInfo implements provider.InstanceGroup
func (g *InstanceGroup) ConnectInfo(ctx context.Context, id string) (provider.ConnectInfo, error) {
	info := provider.ConnectInfo{ConnectorConfig: g.settings.ConnectorConfig}

	if info.OS == "" {
		info.OS = "Linux"
	}
	if info.Arch == "" {
		info.Arch = "amd64"
	}
	if info.Protocol == "" {
		info.Protocol = provider.ProtocolSSH
	}
	if info.Username == "" {
		info.Username = "debian"
	}
	if len(info.Key) == 0 {
		return provider.ConnectInfo{}, fmt.Errorf("ssh key is not configured")
	}
	if info.UseStaticCredentials == false {
		return provider.ConnectInfo{}, fmt.Errorf("must set use_static_credentials for SSH key support")
	}

	vmi, err := g.client.VirtualMachineInstance(g.VmNamespace).Get(ctx, id, k8smetav1.GetOptions{})
	if err != nil {
		return provider.ConnectInfo{}, fmt.Errorf("failed getting vm instance: %w", err)
	}
	for _, ifc := range vmi.Status.Interfaces {
		if ifc.InterfaceName != "docker0" {
			info.InternalAddr = ifc.IP
			break
		}
	}
	if info.InternalAddr == "" {
		return provider.ConnectInfo{}, fmt.Errorf("no suitable interface found in selection of %v", len(vmi.Status.Interfaces))
	}

	g.log.Info("constructed connect info", "vm", id, "ip", info.InternalAddr)

	return info, nil
}

// Decrease implements provider.InstanceGroup
func (g *InstanceGroup) Decrease(ctx context.Context, vms []string) ([]string, error) {
	vmsDeleted := []string{}
	for _, vm := range vms {
		g.log.Info("deleting instance", "vm", vm)
		err := g.client.VirtualMachine(g.VmNamespace).Delete(ctx, vm, k8smetav1.DeleteOptions{})
		if err != nil {
			return vmsDeleted, fmt.Errorf("failed deleting instance %s: %w", vm, err)
		}
		vmsDeleted = append(vmsDeleted, vm)
	}
	return vmsDeleted, nil
}

// Increase implements provider.InstanceGroup
func (g *InstanceGroup) Increase(ctx context.Context, delta int) (int, error) {
	for i := range delta {
		memory, err := resource.ParseQuantity(g.VmRAM)
		if err != nil {
			return i, fmt.Errorf("could not parse RAM quantity '%s': %w", g.VmRAM, err)
		}
		cores, err := strconv.ParseUint(g.VmCPUCores, 10, 0)
		if err != nil {
			return i, fmt.Errorf("could not parse CPU core number '%s': %w", g.VmCPUCores, err)
		}

		strategy := v1.RunStrategyAlways
		newVM := v1.VirtualMachine{
			ObjectMeta: k8smetav1.ObjectMeta{
				GenerateName: g.VmNamePrefix,
				Labels: map[string]string{
					g.VmLabelSelectorKey: g.VmLabelSelectorValue,
				},
			},
			Spec: v1.VirtualMachineSpec{
				RunStrategy: &strategy,
				Template: &v1.VirtualMachineInstanceTemplateSpec{
					ObjectMeta: k8smetav1.ObjectMeta{
						Labels: map[string]string{
							g.VmLabelSelectorKey: g.VmLabelSelectorValue,
						},
					},
					Spec: v1.VirtualMachineInstanceSpec{
						ReadinessProbe: &v1.Probe{
							Handler: v1.Handler{
								Exec: &k8scorev1.ExecAction{
									Command: []string{"sh", "-c", g.VmReadinessProbeScript},
								},
							},
						},
						Domain: v1.DomainSpec{
							Memory: &v1.Memory{
								Guest: &memory,
							},
							CPU: &v1.CPU{
								Cores: uint32(cores),
							},
							Resources: v1.ResourceRequirements{},
							Devices: v1.Devices{
								Disks: []v1.Disk{
									{
										Name:       "containerdisk",
										DiskDevice: v1.DiskDevice{},
									},
									{
										Name:       "cloudinitdisk",
										DiskDevice: v1.DiskDevice{},
									},
								},
							},
						},
						Volumes: []v1.Volume{
							{
								Name: "containerdisk",
								VolumeSource: v1.VolumeSource{
									ContainerDisk: &v1.ContainerDiskSource{
										Image: g.VmRunnerImage,
									},
								},
							},
							{
								Name: "cloudinitdisk",
								VolumeSource: v1.VolumeSource{
									CloudInitNoCloud: &v1.CloudInitNoCloudSource{
										UserData: g.VmCloudInitUserData,
									},
								},
							},
						},
					},
				},
			},
		}

		resultVM, err := g.client.VirtualMachine(g.VmNamespace).Create(ctx, &newVM, k8smetav1.CreateOptions{})
		if err != nil {
			return i, fmt.Errorf("could not create vm: %w", err)
		}
		g.log.Info("created vm", "vm", resultVM.ObjectMeta.Name)
	}

	return delta, nil
}

// Update implements provider.InstanceGroup
func (g *InstanceGroup) Update(ctx context.Context, update func(instance string, state provider.State)) error {

	vms, err := g.client.VirtualMachine(g.VmNamespace).List(
		ctx,
		k8smetav1.ListOptions{LabelSelector: fmt.Sprintf("%s=%s", g.VmLabelSelectorKey, g.VmLabelSelectorValue)},
	)
	if err != nil {
		return fmt.Errorf("failed listing VirtualMachines: %w", err)
	}

	hasReadyCondition := func(conditions []v1.VirtualMachineCondition) bool {
		for _, condition := range conditions {
			if condition.Type == v1.VirtualMachineReady && condition.Status == k8scorev1.ConditionTrue {
				return true
			}
		}
		return false
	}

	for _, vm := range vms.Items {
		var state provider.State

		switch vm.Status.PrintableStatus {
		case v1.VirtualMachineStatusDataVolumeError,
			v1.VirtualMachineStatusCrashLoopBackOff,
			v1.VirtualMachineStatusErrImagePull,
			v1.VirtualMachineStatusImagePullBackOff,
			v1.VirtualMachineStatusPvcNotFound,
			v1.VirtualMachineStatusUnknown:
			state = provider.StateTimeout
		case v1.VirtualMachineStatusMigrating,
			v1.VirtualMachineStatusPaused,
			v1.VirtualMachineStatusProvisioning,
			v1.VirtualMachineStatusStarting,
			v1.VirtualMachineStatusWaitingForVolumeBinding:
			state = provider.StateCreating
		case v1.VirtualMachineStatusRunning:
			if hasReadyCondition(vm.Status.Conditions) {
				state = provider.StateRunning
			} else {
				state = provider.StateCreating
			}
		case v1.VirtualMachineStatusStopped,
			v1.VirtualMachineStatusStopping,
			v1.VirtualMachineStatusTerminating:
			state = provider.StateDeleting
		}
		g.log.Debug("updated state of instance", "vm", vm.GetName(), "gitlabstatus", state, "kubevirtstatus", vm.Status.PrintableStatus)
		update(vm.GetName(), state)
	}

	return nil
}

func (g *InstanceGroup) Shutdown(ctx context.Context) error {
	g.log.Info("deleting instances...")

	err := g.client.VirtualMachine(g.VmNamespace).DeleteCollection(
		ctx,
		k8smetav1.DeleteOptions{},
		k8smetav1.ListOptions{LabelSelector: fmt.Sprintf("%s=%s", g.VmLabelSelectorKey, g.VmLabelSelectorValue)},
	)
	if err != nil {
		return fmt.Errorf("could not delete vm's: %w", err)
	}

	g.log.Info("deleted all instances due to shutdown")

	return nil
}
