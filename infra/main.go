package main

import (
	"encoding/base64"
	"os"

	"github.com/pulumi/pulumi-command/sdk/go/command/local"
	"github.com/pulumi/pulumi-kubernetes/sdk/v4/go/kubernetes"
	"github.com/pulumi/pulumi-kubernetes/sdk/v4/go/kubernetes/apiextensions"
	appsv1 "github.com/pulumi/pulumi-kubernetes/sdk/v4/go/kubernetes/apps/v1"
	corev1 "github.com/pulumi/pulumi-kubernetes/sdk/v4/go/kubernetes/core/v1"
	helmv3 "github.com/pulumi/pulumi-kubernetes/sdk/v4/go/kubernetes/helm/v3"
	helmv4 "github.com/pulumi/pulumi-kubernetes/sdk/v4/go/kubernetes/helm/v4"
	"github.com/pulumi/pulumi-kubernetes/sdk/v4/go/kubernetes/kustomize"
	metav1 "github.com/pulumi/pulumi-kubernetes/sdk/v4/go/kubernetes/meta/v1"
	"github.com/pulumi/pulumi-random/sdk/v4/go/random"
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

		redpandaController, err := helmv3.NewRelease(ctx, "redpanda-controller", &helmv3.ReleaseArgs{
			Chart:     pulumi.String("operator"),
			Namespace: redpandaNs.Metadata.Name(),
			RepositoryOpts: &helmv3.RepositoryOptsArgs{
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

		unixTimestamp, err := local.NewCommand(ctx, "unix-timestamp", &local.CommandArgs{
			Create: pulumi.String("date +%s"),
		})
		if err != nil {
			return err
		}

		redpandaCluster, err := apiextensions.NewCustomResource(ctx, "redpanda-cluster", &apiextensions.CustomResourceArgs{
			ApiVersion: pulumi.String("cluster.redpanda.com/v1alpha2"),
			Kind:       pulumi.String("Redpanda"),
			Metadata: &metav1.ObjectMetaArgs{
				Name:      pulumi.String("redpanda"),
				Namespace: redpandaNs.Metadata.Name(),
			},
			OtherFields: kubernetes.UntypedArgs{
				"spec": pulumi.Map{
					"clusterSpec": pulumi.Map{
						"image": pulumi.Map{
							"repository": pulumi.String("redpandadata/redpanda-nightly"),
							"tag":        pulumi.String("v0.0.0-20250818gitd20fd33"),
						},
						"config": pulumi.Map{
							"cluster": pulumi.Map{
								"development_enable_cloud_topics":                             pulumi.Bool(true),
								"enable_developmental_unrecoverable_data_corrupting_features": unixTimestamp.Stdout,
							},
						},
						"statefulset": pulumi.Map{
							"replicas": pulumi.Int(3),
						},
					},
				},
			},
		}, pulumi.Provider(k8sProvider), pulumi.DependsOn([]pulumi.Resource{redpandaController}))
		if err != nil {
			return err
		}

		password, err := random.NewRandomPassword(ctx, "password", &random.RandomPasswordArgs{
			Length:  pulumi.Int(16),
			Special: pulumi.Bool(true),
		})
		if err != nil {
			return err
		}

		passwordSecret, err := corev1.NewSecret(ctx, "redpanda-admin-user-password", &corev1.SecretArgs{
			Metadata: &metav1.ObjectMetaArgs{
				Name:      pulumi.String("redpanda-admin-user-password"),
				Namespace: redpandaNs.Metadata.Name(),
			},
			Data: pulumi.StringMap{"password": password.Result.ApplyT(func(v string) string {
				return base64.StdEncoding.EncodeToString([]byte(v))
			}).(pulumi.StringOutput)},
		})
		if err != nil {
			return err
		}
		redpandaAdminUser, err := apiextensions.NewCustomResource(ctx, "redpanda-admin-user", &apiextensions.CustomResourceArgs{
			ApiVersion: pulumi.String("cluster.redpanda.com/v1alpha2"),
			Kind:       pulumi.String("User"),
			Metadata: &metav1.ObjectMetaArgs{
				Name:      pulumi.String("admin"),
				Namespace: redpandaNs.Metadata.Name(),
			},
			OtherFields: kubernetes.UntypedArgs{
				"spec": pulumi.Map{
					"cluster": pulumi.Map{
						"clusterRef": pulumi.Map{
							"name": redpandaCluster.Metadata.Name(),
						},
					},
					"authentication": pulumi.Map{
						"type": pulumi.String("scram-sha-256"),
						"password": pulumi.Map{
							"valueFrom": pulumi.Map{
								"secretKeyRef": pulumi.Map{
									"name": passwordSecret.Metadata.Name(),
									"key":  pulumi.String("password"),
								},
							},
						},
					},
					"authorization": pulumi.Map{
						"acls": pulumi.Array{
							pulumi.Map{
								"type": pulumi.String("allow"),
								"resource": pulumi.Map{
									"type":        pulumi.String("topic"),
									"name":        pulumi.String("*"),
									"patternType": pulumi.String("prefixed"),
								},
								"operations": pulumi.StringArray{
									pulumi.String("Read"),
									pulumi.String("Write"),
									pulumi.String("Create"),
									pulumi.String("Delete"),
									pulumi.String("Alter"),
									pulumi.String("Describe"),
									pulumi.String("DescribeConfigs"),
								},
							},
							pulumi.Map{
								"type": pulumi.String("allow"),
								"resource": pulumi.Map{
									"type":        pulumi.String("group"),
									"name":        pulumi.String("*"),
									"patternType": pulumi.String("prefixed"),
								},
								"operations": pulumi.StringArray{
									pulumi.String("Read"),
									pulumi.String("Delete"),
									pulumi.String("Describe"),
								},
							},
						},
					},
				},
			},
		}, pulumi.Provider(k8sProvider), pulumi.DependsOn([]pulumi.Resource{redpandaCluster}))
		if err != nil {
			return err
		}

		file, err := os.ReadFile("./assets/rpcn/simple-producer.yaml")
		if err != nil {
			return err
		}
		configMap, err := corev1.NewConfigMap(ctx, "rpcn-producer-config", &corev1.ConfigMapArgs{
			Metadata: &metav1.ObjectMetaArgs{
				Name:      pulumi.String("rpcn-producer-config"),
				Namespace: redpandaNs.Metadata.Name(),
			},
			Data: pulumi.StringMap{
				"config.yaml": pulumi.String(string(file)),
			},
		})
		if err != nil {
			return err
		}
		_, err = appsv1.NewDeployment(ctx, "rpcn-producer", &appsv1.DeploymentArgs{
			Metadata: &metav1.ObjectMetaArgs{
				Namespace: redpandaNs.Metadata.Name(),
			},
			Spec: appsv1.DeploymentSpecArgs{
				Replicas: pulumi.Int(1),
				Selector: &metav1.LabelSelectorArgs{
					MatchLabels: pulumi.StringMap{
						"app": pulumi.String("rpcn-producer"),
					},
				},
				Template: &corev1.PodTemplateSpecArgs{
					Metadata: &metav1.ObjectMetaArgs{
						Labels: pulumi.StringMap{
							"app": pulumi.String("rpcn-producer"),
						},
					},
					Spec: &corev1.PodSpecArgs{
						Containers: corev1.ContainerArray{
							corev1.ContainerArgs{
								Name:  pulumi.String("rpcn"),
								Image: pulumi.String("redpandadata/connect:4"),
								Args: pulumi.StringArray{
									pulumi.String("run"),
									pulumi.String("/etc/redpanda-connect/config.yaml"),
								},
								VolumeMounts: &corev1.VolumeMountArray{
									&corev1.VolumeMountArgs{
										Name:      pulumi.String("config-volume"),
										MountPath: pulumi.String("/etc/redpanda-connect/"),
										ReadOnly:  pulumi.Bool(true),
									},
								},
								Env: corev1.EnvVarArray{
									&corev1.EnvVarArgs{
										Name: pulumi.String("REDPANDA_BROKERS"),
										// TODO: Is there a better way to compute this?
										Value: pulumi.Sprintf(
											"redpanda-0.%s.%s.svc.cluster.local.:9093",
											redpandaCluster.Metadata.Name().Elem(),
											redpandaCluster.Metadata.Namespace().Elem(),
										),
									},
									&corev1.EnvVarArgs{
										Name: pulumi.String("REDPANDA_CA"),
										ValueFrom: &corev1.EnvVarSourceArgs{
											SecretKeyRef: &corev1.SecretKeySelectorArgs{
												Name: pulumi.String("redpanda-default-root-certificate"),
												Key:  pulumi.String("ca.crt"),
											},
										},
									},
									&corev1.EnvVarArgs{
										Name:  pulumi.String("REDPANDA_USER"),
										Value: redpandaAdminUser.Metadata.Name(),
									},
									&corev1.EnvVarArgs{
										Name: pulumi.String("REDPANDA_PASS"),
										ValueFrom: &corev1.EnvVarSourceArgs{
											SecretKeyRef: &corev1.SecretKeySelectorArgs{
												Name: passwordSecret.Metadata.Name(),
												Key:  pulumi.String("password"),
											},
										},
									},
								},
							},
						},
						Volumes: corev1.VolumeArray{
							corev1.VolumeArgs{
								Name: pulumi.String("config-volume"),
								ConfigMap: &corev1.ConfigMapVolumeSourceArgs{
									Name: configMap.Metadata.Name(),
									Items: &corev1.KeyToPathArray{
										&corev1.KeyToPathArgs{
											Key:  pulumi.String("config.yaml"),
											Path: pulumi.String("config.yaml"),
										},
									},
								},
							},
						},
					},
				},
			},
		}, pulumi.Provider(k8sProvider))
		if err != nil {
			return err
		}

		return nil
	})
}
