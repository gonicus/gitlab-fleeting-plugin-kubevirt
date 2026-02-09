# gitlab-fleeting-plugin-kubevirt

Plugin implementing [Gitlab Fleeting](https://docs.gitlab.com/runner/fleet_scaling/fleeting/) for use with Kubevirt VM's.

## Getting started

1. Get Kubernetes cluster with working Kubevirt
1. Create namespace
    ```
    kubectl create namespace gitlab-kubevirt-test
    ```
1. Generate SSH key pair for automated instance access (e. g. using `ssh-keygen`) and put the result into a secret:
    ```
    $ ssh-keygen -f ssh_id_fleeting -N ''
    ...
    $ kubectl create secret generic -n gitlab-kubevirt-test ssh-key --from-file=ssh-privatekey=ssh_id_fleeting
    ```
1. Add `values.yaml` for Gitlab runner helm chart (See [docs](https://docs.gitlab.com/runner/install/kubernetes/)). Reference `values.yaml` for full docker-autoscaler example (simpler "instance" executor should work as well), note comments; refer to helm chart docs for use of general parameters:
    ```
    gitlabUrl: https://my-gitlab.domain.invalid/ # Replace
    runnerToken: "...token..." # Replace
    certsSecretName: "xyz-certs" # Replace, if any custom certs
    concurrent: 3
    preEntrypointScript: |
      gitlab-runner fleeting install
    serviceAccount:
      create: true
    rbac:
      create: true
      rules:
        - apiGroups: ["kubevirt.io"]
          resources: ["virtualmachines", "virtualmachineinstances"]
          verbs: ["get", "list", "create", "delete"]

    runners:
      executor: "docker-autoscaler"
      config: |
        [[runners]]
          request_concurrency = 2

          [runners.docker]
            image = "alpine:latest"

          [runners.autoscaler]
            plugin = "ghcr.io/gonicus/gitlab-fleeting-plugin-kubevirt:0.0.12" # Or build your own
            capacity_per_instance = 1
            max_use_count = 1
            max_instances = 9
            instance_ready_command = "cloud-init status --wait"
            update_interval_when_expecting = "30s"

          [runners.autoscaler.plugin_config]
            useInClusterConfig = true
            vmLabelKey = "type"
            vmLabelValue = "gitlabfleet"
            vmNamespace = "gitlab-kubevirt-test" # Be sure to set to the same namespace as the helm release
            vmNamePrefix = "gitlab-fleet-vm"
            vmReadinessProbeScript = "cat /tmp/healthy.txt"
            vmRAM = "4Gi"
            vmCPUCores = "4"
            vmRunnerImage = "quay.io/containerdisks/debian:13" # Or - preferably - build your own with all required tooling baked in
            vmCloudInitUserData = '''
        #cloud-config
        ssh_pwauth: False
        ssh_authorized_keys:
          - 'ssh-ed25519 ...' # Put in content of ssh_id_fleeting.pub
        runcmd:
          - apt update && apt -y install curl git qemu-guest-agent ca-certificates docker.io
          - |
              # Add custom CA certificate, if any
              cat > /usr/local/share/ca-certificates/CA.crt <<EOF
              -----BEGIN CERTIFICATE-----
              ...
              -----END CERTIFICATE-----
              EOF
          - update-ca-certificates
          - usermod -aG docker debian
          - systemctl enable --now docker.service
          - touch /tmp/healthy.txt
          - systemctl enable --now qemu-guest-agent'''

          [runners.autoscaler.connector_config]
            username               = "debian"
            key_path               = "/secrets/ssh-privatekey"
            use_static_credentials = true
            timeout                = "5m0s"
            use_external_addr      = false

          [[runners.autoscaler.policy]]
            idle_count = 3
            idle_time  = "20m0s"
            preemptive_mode = true
    secrets:
      - name: "ssh-key"
        items:
          - key: "ssh-privatekey"
            path: "ssh-privatekey"
    ```
1. Install using helm
    ```
    helm repo add gitlab https://charts.gitlab.io
    helm install --namespace gitlab-kubevirt-test gitlab-runner -f values.yaml gitlab/gitlab-runner
    ```
