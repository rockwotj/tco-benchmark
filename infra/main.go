package main

import (
	"github.com/pulumi/pulumi-command/sdk/go/command/local"
	"github.com/pulumi/pulumi-kubernetes/sdk/v3/go/kubernetes/kustomize"
	"github.com/pulumi/pulumi-kubernetes/sdk/v4/go/kubernetes"
	corev1 "github.com/pulumi/pulumi-kubernetes/sdk/v4/go/kubernetes/core/v1"
	helmv4 "github.com/pulumi/pulumi-kubernetes/sdk/v4/go/kubernetes/helm/v4"
	metav1 "github.com/pulumi/pulumi-kubernetes/sdk/v4/go/kubernetes/meta/v1"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
)

func main() {
	pulumi.Run(func(ctx *pulumi.Context) error {
		clusterCmd, err := local.NewCommand(ctx, "create-k3d-cluster", &local.CommandArgs{
			Create: pulumi.String("k3d cluster create tco-benchmark-cluster --agents=5 --verbose --wait --timeout=3m --kubeconfig-update-default=false --kubeconfig-switch-context=false"),
			Delete: pulumi.String("k3d cluster delete tco-benchmark-cluster"),
			Interpreter: pulumi.StringArray{
				pulumi.String("/bin/bash"),
				pulumi.String("-c"),
			},
		})
		if err != nil {
			return err
		}

		kubeConfigCmd, err := local.NewCommand(ctx, "get-k3d-kubeconfig", &local.CommandArgs{
			Create: pulumi.String("k3d kubeconfig get tco-benchmark-cluster"),
		}, pulumi.DependsOn([]pulumi.Resource{clusterCmd}))
		if err != nil {
			return err
		}

		// Create a Kubernetes provider instance that uses our cluster
		k8sProvider, err := kubernetes.NewProvider(ctx, "k8s-provider", &kubernetes.ProviderArgs{
			Kubeconfig: kubeConfigCmd.Stdout,
		}, pulumi.DependsOn([]pulumi.Resource{kubeConfigCmd}))
		if err != nil {
			return err
		}

		prometheusNs, err := corev1.NewNamespace(ctx, "prometheus-namespace", &corev1.NamespaceArgs{
			Metadata: &metav1.ObjectMetaArgs{
				Name: pulumi.String("prometheus"),
			},
		}, pulumi.Provider(k8sProvider))
		if err != nil {
			return err
		}

		prometheus, err := kustomize.NewDirectory(ctx, "prometheus", kustomize.DirectoryArgs{
			Directory: pulumi.String("./assets/prometheus/"),
		}, pulumi.Provider(k8sProvider), pulumi.DependsOn([]pulumi.Resource{prometheusNs}))
		if err != nil {
			return err
		}

		chartManagerNs, err := corev1.NewNamespace(ctx, "cert-manager-namespace", &corev1.NamespaceArgs{
			Metadata: &metav1.ObjectMetaArgs{
				Name: pulumi.String("cert-manager"),
			},
		}, pulumi.Provider(k8sProvider))
		if err != nil {
			return err
		}

		certManager, err := helmv4.NewChart(ctx, "cert-manager-chart", &helmv4.ChartArgs{
			Chart:     pulumi.String("cert-manager"),
			Namespace: chartManagerNs.Metadata.Name(),
			RepositoryOpts: &helmv4.RepositoryOptsArgs{
				Repo: pulumi.String("https://charts.jetstack.io"),
			},
			Version: pulumi.String("v1.16.1"),
			Values: pulumi.Map{
				"crds": pulumi.Map{
					"enabled": pulumi.Bool(true),
				},
			},
		}, pulumi.Provider(k8sProvider), pulumi.DependsOn([]pulumi.Resource{prometheus}))
		if err != nil {
			return err
		}

		redpandaNs, err := corev1.NewNamespace(ctx, "redpanda-namespace", &corev1.NamespaceArgs{
			Metadata: &metav1.ObjectMetaArgs{
				Name: pulumi.String("redpanda"),
			},
		}, pulumi.Provider(k8sProvider))
		if err != nil {
			return err
		}

		redpandaController, err := helmv4.NewChart(ctx, "redpanda-controller", &helmv4.ChartArgs{
			Chart:     pulumi.String("operator"),
			Namespace: redpandaNs.Metadata.Name(),
			RepositoryOpts: &helmv4.RepositoryOptsArgs{
				Repo: pulumi.String("https://charts.redpanda.com"),
			},
			Version: pulumi.String("25.1.3"),
			Values: pulumi.Map{
				"crds": pulumi.Map{
					"enabled": pulumi.Bool(true),
				},
			},
		}, pulumi.Provider(k8sProvider), pulumi.DependsOn([]pulumi.Resource{certManager, prometheus}))
		if err != nil {
			return err
		}

		_ = redpandaController

		return nil
	})
}
