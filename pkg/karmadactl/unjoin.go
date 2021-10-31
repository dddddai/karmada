package karmadactl

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	kubeclient "k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/klog/v2"

	clusterv1alpha1 "github.com/karmada-io/karmada/pkg/apis/cluster/v1alpha1"
	karmadaclientset "github.com/karmada-io/karmada/pkg/generated/clientset/versioned"
	"github.com/karmada-io/karmada/pkg/karmadactl/options"
	"github.com/karmada-io/karmada/pkg/util"
	"github.com/karmada-io/karmada/pkg/util/names"
)

var (
	unjoinShort   = `Remove the registration of a cluster from control plane`
	unjoinLong    = `Unjoin removes the registration of a cluster from control plane.`
	unjoinExample = `
%s unjoin CLUSTER_NAME --cluster-kubeconfig=<KUBECONFIG>
`
)

// NewCmdUnjoin defines the `unjoin` command that removes registration of a cluster from control plane.
func NewCmdUnjoin(cmdOut io.Writer, karmadaConfig KarmadaConfig, cmdStr string) *cobra.Command {
	opts := CommandUnjoinOption{}

	cmd := &cobra.Command{
		Use:     "unjoin CLUSTER_NAME --cluster-kubeconfig=<KUBECONFIG>",
		Short:   unjoinShort,
		Long:    unjoinLong,
		Example: fmt.Sprintf(unjoinExample, cmdStr),
		Run: func(cmd *cobra.Command, args []string) {
			err := opts.Complete(args)
			if err != nil {
				klog.Fatalf("Error: %v", err)
			}

			err = RunUnjoin(cmdOut, karmadaConfig, opts)
			if err != nil {
				klog.Fatalf("Error: %v", err)
			}
		},
	}

	flags := cmd.Flags()
	opts.AddFlags(flags)

	return cmd
}

// CommandUnjoinOption holds all command options.
type CommandUnjoinOption struct {
	options.GlobalCommandOptions

	// ClusterName is the cluster's name that we are going to unjoin.
	ClusterName string

	// ClusterContext is the cluster's context that we are going to unjoin.
	ClusterContext string

	// ClusterKubeConfig is the cluster's kubeconfig path.
	ClusterKubeConfig string

	// AgentName is the name of agent that deployed in pull mode cluster.
	AgentName string

	forceDeletion bool
}

// Complete ensures that options are valid and marshals them if necessary.
func (j *CommandUnjoinOption) Complete(args []string) error {
	// Get cluster name from the command args.
	if len(args) == 0 {
		return errors.New("cluster name is required")
	}
	j.ClusterName = args[0]

	// If '--cluster-context' not specified, take the cluster name as the context.
	if len(j.ClusterContext) == 0 {
		j.ClusterContext = j.ClusterName
	}

	return nil
}

// AddFlags adds flags to the specified FlagSet.
func (j *CommandUnjoinOption) AddFlags(flags *pflag.FlagSet) {
	j.GlobalCommandOptions.AddFlags(flags)

	flags.StringVar(&j.ClusterContext, "cluster-context", "",
		"Context name of cluster in kubeconfig. Only works when there are multiple contexts in the kubeconfig.")
	flags.StringVar(&j.ClusterKubeConfig, "cluster-kubeconfig", "",
		"Path of the cluster's kubeconfig.")
	flags.StringVar(&j.AgentName, "agent-name", "karmada-agent",
		"Name of agent in pull mode cluster. Only works when the unjoining cluster's sync mode is pull.")
	flags.BoolVar(&j.forceDeletion, "force", false,
		"Delete cluster and secret resources even if resources in the cluster targeted for unjoin are not removed successfully.")
}

// RunUnjoin is the implementation of the 'unjoin' command.
func RunUnjoin(cmdOut io.Writer, karmadaConfig KarmadaConfig, opts CommandUnjoinOption) error {
	klog.V(1).Infof("unjoining cluster. cluster name: %s", opts.ClusterName)
	klog.V(1).Infof("unjoining cluster. cluster namespace: %s", opts.ClusterNamespace)

	// Get control plane kube-apiserver client
	controlPlaneRestConfig, err := karmadaConfig.GetRestConfig(opts.KarmadaContext, opts.KubeConfig)
	if err != nil {
		klog.Errorf("failed to get control plane rest config. context: %s, kube-config: %s, error: %v",
			opts.KarmadaContext, opts.KubeConfig, err)
		return err
	}

	var clusterConfig *rest.Config
	if opts.ClusterKubeConfig != "" {
		// Get cluster config
		clusterConfig, err = karmadaConfig.GetRestConfig(opts.ClusterContext, opts.ClusterKubeConfig)
		if err != nil {
			klog.V(1).Infof("failed to get unjoining cluster config. error: %v", err)
			return err
		}
	}

	return UnJoinCluster(controlPlaneRestConfig, clusterConfig, opts)
}

// UnJoinCluster unJoin the cluster from karmada.
func UnJoinCluster(controlPlaneRestConfig, clusterConfig *rest.Config, opts CommandUnjoinOption) (err error) {
	controlPlaneKarmadaClient := karmadaclientset.NewForConfigOrDie(controlPlaneRestConfig)

	// Attempt to delete the cluster role, cluster rolebindings and service account from the unjoining cluster
	// if user provides the kubeconfig of cluster
	if clusterConfig != nil {
		clusterKubeClient := kubeclient.NewForConfigOrDie(clusterConfig)

		klog.V(1).Infof("unjoining cluster config. endpoint: %s", clusterConfig.Host)

		// delete RBAC resource from unjoining cluster
		err = deleteRBACResources(clusterKubeClient, opts.ClusterName, opts.forceDeletion, opts.DryRun)
		if err != nil {
			klog.Errorf("Failed to delete RBAC resource in unjoining cluster %q: %v", opts.ClusterName, err)
			return err
		}

		// delete service account from unjoining cluster
		err = deleteServiceAccount(clusterKubeClient, opts.ClusterNamespace, opts.ClusterName, opts.forceDeletion, opts.DryRun)
		if err != nil {
			klog.Errorf("Failed to delete service account in unjoining cluster %q: %v", opts.ClusterName, err)
			return err
		}

		// delete namespace from unjoining cluster
		err = deleteNamespaceFromUnjoinCluster(clusterKubeClient, opts.ClusterNamespace, opts.ClusterName, opts.forceDeletion, opts.DryRun)
		if err != nil {
			klog.Errorf("Failed to delete namespace in unjoining cluster %q: %v", opts.ClusterName, err)
			return err
		}

		// If cluster sync mode is pull, delete resources associated with agent from the unjoining cluster
		if isPullMode(controlPlaneKarmadaClient, opts.ClusterName, opts.DryRun) {
			if opts.ClusterName == util.KarmadaHost {
				klog.Warningf("Cannot clean up agent resources in %s, please clean up them by yourself", util.KarmadaHost)
			} else if err = deleteAgent(clusterKubeClient, opts.AgentName, opts.ClusterName, opts.forceDeletion); err != nil {
				klog.Errorf("Failed to clean up agent resources in unjoining cluster %q: %v", opts.ClusterName, err)
				return err
			}
		}
	}
	// delete the cluster object in host cluster that associates the unjoining cluster
	err = deleteClusterObject(controlPlaneKarmadaClient, opts.ClusterName, opts.DryRun)
	if err != nil {
		klog.Errorf("Failed to delete cluster object. cluster name: %s, error: %v", opts.ClusterName, err)
		return err
	}

	return nil
}

// deleteRBACResources deletes the cluster role, cluster rolebindings from the unjoining cluster.
func deleteRBACResources(clusterKubeClient kubeclient.Interface, unjoiningClusterName string, forceDeletion, dryRun bool) error {
	if dryRun {
		return nil
	}

	serviceAccountName := names.GenerateServiceAccountName(unjoiningClusterName)
	clusterRoleName := names.GenerateRoleName(serviceAccountName)
	clusterRoleBindingName := clusterRoleName

	err := util.DeleteClusterRoleBinding(clusterKubeClient, clusterRoleBindingName)
	if err != nil {
		if !forceDeletion {
			return err
		}
		klog.Errorf("Force deletion. Could not delete cluster role binding %q for service account %q in unjoining cluster %q: %v.", clusterRoleBindingName, serviceAccountName, unjoiningClusterName, err)
	}

	err = util.DeleteClusterRole(clusterKubeClient, clusterRoleName)
	if err != nil {
		if !forceDeletion {
			return err
		}
		klog.Errorf("Force deletion. Could not delete cluster role %q for service account %q in unjoining cluster %q: %v.", clusterRoleName, serviceAccountName, unjoiningClusterName, err)
	}

	return nil
}

// deleteServiceAccount deletes the service account from the unjoining cluster.
func deleteServiceAccount(clusterKubeClient kubeclient.Interface, namespace, unjoiningClusterName string, forceDeletion, dryRun bool) error {
	if dryRun {
		return nil
	}

	serviceAccountName := names.GenerateServiceAccountName(unjoiningClusterName)
	err := util.DeleteServiceAccount(clusterKubeClient, namespace, serviceAccountName)
	if err != nil {
		if !forceDeletion {
			return err
		}
		klog.Errorf("Force deletion. Could not delete service account %q in unjoining cluster %q: %v.", serviceAccountName, unjoiningClusterName, err)
	}

	return nil
}

// deleteNSFromUnjoinCluster deletes the namespace from the unjoining cluster.
func deleteNamespaceFromUnjoinCluster(clusterKubeClient kubeclient.Interface, namespace, unjoiningClusterName string, forceDeletion, dryRun bool) error {
	if dryRun {
		return nil
	}

	err := util.DeleteNamespace(clusterKubeClient, namespace)
	if err != nil {
		if !forceDeletion {
			return err
		}
		klog.Errorf("Force deletion. Could not delete namespace %q in unjoining cluster %q: %v.", namespace, unjoiningClusterName, err)
	}

	return nil
}

// deleteClusterObject delete the cluster object in host cluster that associates the unjoining cluster
func deleteClusterObject(controlPlaneKarmadaClient *karmadaclientset.Clientset, clusterName string, dryRun bool) error {
	if dryRun {
		return nil
	}

	err := controlPlaneKarmadaClient.ClusterV1alpha1().Clusters().Delete(context.TODO(), clusterName, metav1.DeleteOptions{})
	if apierrors.IsNotFound(err) {
		return nil
	}
	if err != nil {
		klog.Errorf("Failed to delete cluster object. cluster name: %s, error: %v", clusterName, err)
		return err
	}

	// make sure the given cluster object has been deleted
	err = wait.Poll(1*time.Second, 1*time.Minute, func() (done bool, err error) {
		_, err = controlPlaneKarmadaClient.ClusterV1alpha1().Clusters().Get(context.TODO(), clusterName, metav1.GetOptions{})
		if apierrors.IsNotFound(err) {
			return true, nil
		}
		if err != nil {
			klog.Errorf("Failed to get cluster %s. err: %v", clusterName, err)
			return false, err
		}
		klog.Infof("Waiting for the cluster object %s to be deleted", clusterName)
		return false, nil
	})
	if err != nil {
		klog.Errorf("Failed to delete cluster object. cluster name: %s, error: %v", clusterName, err)
		return err
	}

	return nil
}

// isPullMode indicates if the unjoining cluster's sync mode is pull
func isPullMode(controlPlaneKarmadaClient *karmadaclientset.Clientset, clusterName string, dryRun bool) bool {
	if dryRun {
		return false
	}
	cluster, err := controlPlaneKarmadaClient.ClusterV1alpha1().Clusters().Get(context.TODO(), clusterName, metav1.GetOptions{})
	if err != nil {
		klog.Errorf("Failed to get unjoining cluster %q: %v", clusterName, err)
		return false
	}
	return cluster.Spec.SyncMode == clusterv1alpha1.Pull
}

// deleteAgent deletes resources associated with agent from the unjoining cluster.
func deleteAgent(clusterKubeClient kubeclient.Interface, agentName, unjoiningClusterName string, forceDeletion bool) error {
	// clean up resources associated with agent
	// 1.delete cluster role and cluster role binding, their names must be the same as agent name
	// 2.delete karmada-system namespace
	err := clusterKubeClient.RbacV1().ClusterRoles().Delete(context.TODO(), agentName, metav1.DeleteOptions{})
	if err != nil && !apierrors.IsNotFound(err) {
		if !forceDeletion {
			return err
		}
		klog.Errorf("Force deletion. Could not delete cluster role %q in unjoining cluster %q: %v.", agentName, unjoiningClusterName, err)
	}

	err = clusterKubeClient.RbacV1().ClusterRoleBindings().Delete(context.TODO(), agentName, metav1.DeleteOptions{})
	if err != nil && !apierrors.IsNotFound(err) {
		if !forceDeletion {
			return err
		}
		klog.Errorf("Force deletion. Could not delete cluster role binding %q in unjoining cluster %q: %v.", agentName, unjoiningClusterName, err)
	}

	err = clusterKubeClient.CoreV1().Namespaces().Delete(context.TODO(), util.NamespaceKarmadaSystem, metav1.DeleteOptions{})
	if err != nil && !apierrors.IsNotFound(err) {
		if !forceDeletion {
			return err
		}
		klog.Errorf("Force deletion. Could not delete namespace %q in unjoining cluster %q: %v.", util.NamespaceKarmadaSystem, unjoiningClusterName, err)
	}
	return nil
}
