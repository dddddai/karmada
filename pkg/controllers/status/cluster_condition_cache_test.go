package status

import (
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	clusterv1alpha1 "github.com/karmada-io/karmada/pkg/apis/cluster/v1alpha1"
)

func TestThresholdAdjustedReadyCondition(t *testing.T) {
	clusterSuccessThreshold := 30 * time.Second
	clusterFailureThreshold := 30 * time.Second

	tests := []struct {
		name              string
		clusterData       *clusterData
		currentCondition  *metav1.Condition
		observedCondition *metav1.Condition
		expectedCondition *metav1.Condition
	}{
		{
			name:             "cluster just joined in ready state",
			clusterData:      nil, // no cache yet
			currentCondition: nil, // no condition was set on cluster object yet
			observedCondition: &metav1.Condition{
				Type:   clusterv1alpha1.ClusterConditionReady,
				Status: metav1.ConditionTrue,
			},
			expectedCondition: &metav1.Condition{
				Type:   clusterv1alpha1.ClusterConditionReady,
				Status: metav1.ConditionTrue,
			},
		},
		{
			name:             "cluster just joined in not-ready state",
			clusterData:      nil, // no cache yet
			currentCondition: nil, // no condition was set on cluster object yet
			observedCondition: &metav1.Condition{
				Type:   clusterv1alpha1.ClusterConditionReady,
				Status: metav1.ConditionFalse,
			},
			expectedCondition: &metav1.Condition{
				Type:   clusterv1alpha1.ClusterConditionReady,
				Status: metav1.ConditionFalse,
			},
		},
		{
			name: "cluster stays ready",
			clusterData: &clusterData{
				readyCondition: metav1.ConditionTrue,
			},
			currentCondition: &metav1.Condition{
				Type:   clusterv1alpha1.ClusterConditionReady,
				Status: metav1.ConditionTrue,
			},
			observedCondition: &metav1.Condition{
				Type:   clusterv1alpha1.ClusterConditionReady,
				Status: metav1.ConditionTrue,
			},
			expectedCondition: &metav1.Condition{
				Type:   clusterv1alpha1.ClusterConditionReady,
				Status: metav1.ConditionTrue,
			},
		},
		{
			name: "cluster becomes not ready but still not reach failure threshold",
			clusterData: &clusterData{
				readyCondition:     metav1.ConditionFalse,
				thresholdStartTime: time.Now().Add(-clusterFailureThreshold / 2),
			},
			currentCondition: &metav1.Condition{
				Type:   clusterv1alpha1.ClusterConditionReady,
				Status: metav1.ConditionTrue,
			},
			observedCondition: &metav1.Condition{
				Type:   clusterv1alpha1.ClusterConditionReady,
				Status: metav1.ConditionFalse,
			},
			expectedCondition: &metav1.Condition{
				Type:   clusterv1alpha1.ClusterConditionReady,
				Status: metav1.ConditionTrue,
			},
		},
		{
			name: "cluster becomes not ready and reaches failure threshold",
			clusterData: &clusterData{
				readyCondition:     metav1.ConditionFalse,
				thresholdStartTime: time.Now().Add(-clusterFailureThreshold),
			},
			currentCondition: &metav1.Condition{
				Type:   clusterv1alpha1.ClusterConditionReady,
				Status: metav1.ConditionTrue,
			},
			observedCondition: &metav1.Condition{
				Type:   clusterv1alpha1.ClusterConditionReady,
				Status: metav1.ConditionFalse,
			},
			expectedCondition: &metav1.Condition{
				Type:   clusterv1alpha1.ClusterConditionReady,
				Status: metav1.ConditionFalse,
			},
		},
		{
			name: "cluster stays not ready",
			clusterData: &clusterData{
				readyCondition:     metav1.ConditionFalse,
				thresholdStartTime: time.Now().Add(-2 * clusterFailureThreshold),
			},
			currentCondition: &metav1.Condition{
				Type:   clusterv1alpha1.ClusterConditionReady,
				Status: metav1.ConditionFalse,
			},
			observedCondition: &metav1.Condition{
				Type:   clusterv1alpha1.ClusterConditionReady,
				Status: metav1.ConditionFalse,
			},
			expectedCondition: &metav1.Condition{
				Type:   clusterv1alpha1.ClusterConditionReady,
				Status: metav1.ConditionFalse,
			},
		},
		{
			name: "cluster recovers but still not reach success threshold",
			clusterData: &clusterData{
				readyCondition:     metav1.ConditionTrue,
				thresholdStartTime: time.Now().Add(-clusterSuccessThreshold / 2),
			},
			currentCondition: &metav1.Condition{
				Type:   clusterv1alpha1.ClusterConditionReady,
				Status: metav1.ConditionFalse,
			},
			observedCondition: &metav1.Condition{
				Type:   clusterv1alpha1.ClusterConditionReady,
				Status: metav1.ConditionTrue,
			},
			expectedCondition: &metav1.Condition{
				Type:   clusterv1alpha1.ClusterConditionReady,
				Status: metav1.ConditionFalse,
			},
		},
		{
			name: "cluster recovers and reaches success threshold",
			clusterData: &clusterData{
				readyCondition:     metav1.ConditionTrue,
				thresholdStartTime: time.Now().Add(-clusterSuccessThreshold),
			},
			currentCondition: &metav1.Condition{
				Type:   clusterv1alpha1.ClusterConditionReady,
				Status: metav1.ConditionFalse,
			},
			observedCondition: &metav1.Condition{
				Type:   clusterv1alpha1.ClusterConditionReady,
				Status: metav1.ConditionTrue,
			},
			expectedCondition: &metav1.Condition{
				Type:   clusterv1alpha1.ClusterConditionReady,
				Status: metav1.ConditionTrue,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cache := clusterConditionStore{
				successThreshold: clusterSuccessThreshold,
				failureThreshold: clusterFailureThreshold,
			}

			if tt.clusterData != nil {
				cache.update("member", tt.clusterData)
			}

			cluster := &clusterv1alpha1.Cluster{}
			cluster.Name = "member"
			if tt.currentCondition != nil {
				meta.SetStatusCondition(&cluster.Status.Conditions, *tt.currentCondition)
			}

			thresholdReadyCondition := cache.thresholdAdjustedReadyCondition(cluster, tt.observedCondition)

			if tt.expectedCondition.Status != thresholdReadyCondition.Status {
				t.Fatalf("expected: %s, but got: %s", tt.expectedCondition.Status, thresholdReadyCondition.Status)
			}
		})
	}
}

func TestThresholdAdjustedReadyCondition2(t *testing.T) {
	tests := []struct {
		online        bool
		healthy       bool
		expectedReady metav1.ConditionStatus
	}{
		{
			// cluster is healthy now
			online:        true,
			healthy:       true,
			expectedReady: metav1.ConditionTrue,
		},
		{
			// cluster becomes unhealthy
			online:        true,
			healthy:       false,
			expectedReady: metav1.ConditionTrue,
		},
		{
			// the first retry
			online:        false,
			healthy:       true,
			expectedReady: metav1.ConditionTrue,
		},
		{
			// the second retry
			online:        false,
			healthy:       false,
			expectedReady: metav1.ConditionTrue,
		},
		{
			// the third retry
			// cluster is still unhealthy after the threshold, set cluster status to not ready
			online:        false,
			healthy:       false,
			expectedReady: metav1.ConditionFalse,
		},
		{
			// cluster is still unhealthy after the threshold, set cluster status to not ready
			online:        false,
			healthy:       false,
			expectedReady: metav1.ConditionFalse,
		},
		{
			// cluster recoverd
			online:        true,
			healthy:       true,
			expectedReady: metav1.ConditionFalse,
		},
		{
			// cluster becomes unhealthy
			online:        false,
			healthy:       false,
			expectedReady: metav1.ConditionFalse,
		},
		{
			// cluster recoverd
			online:        true,
			healthy:       true,
			expectedReady: metav1.ConditionFalse,
		},
		// {
		// 	// cluster becomes unhealthy
		// 	online:        true,
		// 	healthy:       true,
		// 	expectedReady: metav1.ConditionFalse,
		// },
		{
			// the first retry
			online:        true,
			healthy:       true,
			expectedReady: metav1.ConditionFalse,
		},
		{
			// the second retry
			online:        true,
			healthy:       true,
			expectedReady: metav1.ConditionFalse,
		},
		{
			// the third retry
			// cluster is still unhealthy after the threshold, set cluster status to not ready
			online:        true,
			healthy:       true,
			expectedReady: metav1.ConditionTrue,
		},
	}

	// check frequency
	frequency := 100 * time.Millisecond
	c := ClusterStatusController{
		clusterConditionCache: clusterConditionStore{
			successThreshold: 300 * time.Millisecond,
			failureThreshold: 300 * time.Millisecond,
		},
	}
	cluster := &clusterv1alpha1.Cluster{}

	for i, tt := range tests {
		readyCondition := generateReadyCondition(tt.online, tt.healthy)
		thresholdReadyCondition := c.clusterConditionCache.thresholdAdjustedReadyCondition(cluster, &readyCondition)
		if tt.expectedReady != thresholdReadyCondition.Status {
			t.Errorf("retryOnFailure(%d) failed, want: %v, got: %v", i, tt.expectedReady, thresholdReadyCondition.Status)
		}
		meta.SetStatusCondition(&cluster.Status.Conditions, *thresholdReadyCondition)
		time.Sleep(frequency)
	}
}
