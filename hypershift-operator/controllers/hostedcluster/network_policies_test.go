package hostedcluster

import (
	"context"
	"testing"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/hypershift-operator/controllers/manifests"
	"github.com/openshift/hypershift/hypershift-operator/controllers/manifests/networkpolicy"
	fakecapabilities "github.com/openshift/hypershift/support/capabilities/fake"
	"github.com/openshift/hypershift/support/upsert"

	configv1 "github.com/openshift/api/config/v1"

	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	"github.com/blang/semver"
)

func TestReconcileNetworkPolicies_GCP_PrivateRouter(t *testing.T) {
	// Create test GCP HostedCluster
	hcluster := &hyperv1.HostedCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-gcp-cluster",
			Namespace: "test-namespace",
		},
		Spec: hyperv1.HostedClusterSpec{
			Platform: hyperv1.PlatformSpec{
				Type: hyperv1.GCPPlatform,
			},
		},
	}

	hcp := &hyperv1.HostedControlPlane{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-gcp-cluster",
			Namespace: manifests.HostedControlPlaneNamespace(hcluster.Namespace, hcluster.Name),
		},
	}

	// Create test environment
	kubernetesEndpoint := &corev1.Endpoints{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "kubernetes",
			Namespace: "default",
		},
		Subsets: []corev1.EndpointSubset{
			{
				Addresses: []corev1.EndpointAddress{
					{IP: "10.0.0.1"},
				},
			},
		},
	}

	managementClusterNetwork := &configv1.Network{
		ObjectMeta: metav1.ObjectMeta{
			Name: "cluster",
		},
		Spec: configv1.NetworkSpec{
			ClusterNetwork: []configv1.ClusterNetworkEntry{
				{CIDR: "10.128.0.0/14"},
			},
			ServiceNetwork: []string{"172.30.0.0/16"},
		},
	}

	// Setup fake client
	scheme := runtime.NewScheme()
	if err := hyperv1.AddToScheme(scheme); err != nil {
		t.Fatalf("Failed to add hyperv1 scheme: %v", err)
	}
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatalf("Failed to add corev1 scheme: %v", err)
	}
	if err := configv1.AddToScheme(scheme); err != nil {
		t.Fatalf("Failed to add configv1 scheme: %v", err)
	}
	if err := networkingv1.AddToScheme(scheme); err != nil {
		t.Fatalf("Failed to add networkingv1 scheme: %v", err)
	}

	objs := []client.Object{kubernetesEndpoint, managementClusterNetwork}
	fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(objs...).Build()

	reconciler := &HostedClusterReconciler{
		Client:                        fakeClient,
		ManagementClusterCapabilities: fakecapabilities.NewSupportAllExcept(),
	}

	// Track created network policies
	createdNetworkPolicies := make(map[string]*networkingv1.NetworkPolicy)
	createOrUpdate := upsert.CreateOrUpdateFN(func(ctx context.Context, client client.Client, obj client.Object, f controllerutil.MutateFn) (controllerutil.OperationResult, error) {
		if netPol, ok := obj.(*networkingv1.NetworkPolicy); ok {
			if err := f(); err != nil {
				return controllerutil.OperationResultNone, err
			}
			createdNetworkPolicies[netPol.Name] = netPol
		}
		return controllerutil.OperationResultCreated, nil
	})

	// Execute the test
	ctx := context.Background()
	log := ctrl.Log.WithName("test-gcp")
	version := semver.MustParse("4.15.0")

	err := reconciler.reconcileNetworkPolicies(ctx, log, createOrUpdate, hcluster, hcp, version, false)
	if err != nil {
		t.Fatalf("reconcileNetworkPolicies failed: %v", err)
	}

	// Verify private-router NetworkPolicy is created for GCP
	privateRouterPolicy, exists := createdNetworkPolicies["private-router"]
	if !exists {
		t.Error("Expected private-router NetworkPolicy to be created for GCP platform")
	} else {
		verifyPrivateRouterNetworkPolicy(t, privateRouterPolicy)
	}

	// Verify core policies are created
	expectedPolicies := []string{"openshift-ingress", "same-namespace", "kas", "openshift-monitoring"}
	for _, policyName := range expectedPolicies {
		if _, exists := createdNetworkPolicies[policyName]; !exists {
			t.Errorf("Expected %s NetworkPolicy to be created", policyName)
		}
	}
}

// verifyPrivateRouterNetworkPolicy verifies that the private-router NetworkPolicy has the correct configuration
func verifyPrivateRouterNetworkPolicy(t *testing.T, policy *networkingv1.NetworkPolicy) {
	// Verify policy types
	expectedTypes := []networkingv1.PolicyType{networkingv1.PolicyTypeIngress, networkingv1.PolicyTypeEgress}
	if len(policy.Spec.PolicyTypes) != len(expectedTypes) {
		t.Errorf("Expected %d policy types, got %d", len(expectedTypes), len(policy.Spec.PolicyTypes))
	}
	for i, expectedType := range expectedTypes {
		if i >= len(policy.Spec.PolicyTypes) || policy.Spec.PolicyTypes[i] != expectedType {
			t.Errorf("Expected policy type %s at index %d, got %s", expectedType, i, policy.Spec.PolicyTypes[i])
		}
	}

	// Verify pod selector
	expectedLabels := map[string]string{"app": "private-router"}
	if len(policy.Spec.PodSelector.MatchLabels) != len(expectedLabels) {
		t.Errorf("Expected %d pod selector labels, got %d", len(expectedLabels), len(policy.Spec.PodSelector.MatchLabels))
	}
	for key, expectedValue := range expectedLabels {
		if actualValue, exists := policy.Spec.PodSelector.MatchLabels[key]; !exists || actualValue != expectedValue {
			t.Errorf("Expected pod selector label %s=%s, got %s=%s", key, expectedValue, key, actualValue)
		}
	}

	// Verify ingress rules
	if len(policy.Spec.Ingress) == 0 {
		t.Error("Expected at least one ingress rule")
	} else {
		ingressRule := policy.Spec.Ingress[0]
		if len(ingressRule.Ports) != 2 {
			t.Errorf("Expected 2 ingress ports, got %d", len(ingressRule.Ports))
		} else {
			// Check for ports 8080 and 8443
			expectedPorts := []int32{8080, 8443}
			actualPorts := make([]int32, len(ingressRule.Ports))
			for i, port := range ingressRule.Ports {
				if port.Port != nil {
					actualPorts[i] = port.Port.IntVal
				}
			}
			for _, expectedPort := range expectedPorts {
				found := false
				for _, actualPort := range actualPorts {
					if actualPort == expectedPort {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("Expected ingress port %d not found", expectedPort)
				}
			}
		}
	}

	// Verify egress rules exist (detailed verification would require more complex setup)
	if len(policy.Spec.Egress) == 0 {
		t.Error("Expected at least one egress rule")
	}
}

func TestGCPPrivateRouterNetworkPolicy_IngressOnly(t *testing.T) {
	// Test GCP platform with ingressOnly parameter functionality
	hcluster := &hyperv1.HostedCluster{
		Spec: hyperv1.HostedClusterSpec{
			Platform: hyperv1.PlatformSpec{
				Type: hyperv1.GCPPlatform,
			},
		},
	}

	kubernetesEndpoint := &corev1.Endpoints{
		Subsets: []corev1.EndpointSubset{
			{
				Addresses: []corev1.EndpointAddress{
					{IP: "10.0.0.1"},
				},
			},
		},
	}

	policy := networkpolicy.PrivateRouterNetworkPolicy("test-namespace")

	// Test with ingressOnly = true
	err := reconcilePrivateRouterNetworkPolicy(policy, hcluster, kubernetesEndpoint, false, nil, true, "")
	if err != nil {
		t.Fatalf("reconcilePrivateRouterNetworkPolicy with ingressOnly=true failed: %v", err)
	}

	// Verify only ingress policy type is set
	if len(policy.Spec.PolicyTypes) != 1 || policy.Spec.PolicyTypes[0] != networkingv1.PolicyTypeIngress {
		t.Error("Expected only Ingress policy type when ingressOnly=true")
	}

	// Verify no egress rules
	if len(policy.Spec.Egress) != 0 {
		t.Error("Expected no egress rules when ingressOnly=true")
	}

	// Verify GCP-specific port configuration
	if len(policy.Spec.Ingress) > 0 {
		ingressRule := policy.Spec.Ingress[0]
		if len(ingressRule.Ports) == 2 {
			// Verify ports 8080 and 8443 are present
			foundPorts := make(map[int32]bool)
			for _, port := range ingressRule.Ports {
				if port.Port != nil {
					foundPorts[port.Port.IntVal] = true
				}
			}
			if !foundPorts[8080] || !foundPorts[8443] {
				t.Error("Expected GCP private router to have ports 8080 and 8443")
			}
		}
	}
}

func TestReconcileNetworkPolicies_Azure(t *testing.T) {
	// Create test Azure HostedCluster
	hcluster := &hyperv1.HostedCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-azure-cluster",
			Namespace: "test-namespace",
		},
		Spec: hyperv1.HostedClusterSpec{
			Platform: hyperv1.PlatformSpec{
				Type: hyperv1.AzurePlatform,
			},
		},
	}

	hcp := &hyperv1.HostedControlPlane{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-azure-cluster",
			Namespace: manifests.HostedControlPlaneNamespace(hcluster.Namespace, hcluster.Name),
		},
	}

	// Create test environment
	kubernetesEndpoint := &corev1.Endpoints{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "kubernetes",
			Namespace: "default",
		},
		Subsets: []corev1.EndpointSubset{
			{
				Addresses: []corev1.EndpointAddress{
					{IP: "10.0.0.1"},
				},
			},
		},
	}

	managementClusterNetwork := &configv1.Network{
		ObjectMeta: metav1.ObjectMeta{
			Name: "cluster",
		},
		Spec: configv1.NetworkSpec{
			ClusterNetwork: []configv1.ClusterNetworkEntry{
				{CIDR: "10.128.0.0/14"},
			},
			ServiceNetwork: []string{"172.30.0.0/16"},
		},
	}

	// Setup fake client
	scheme := runtime.NewScheme()
	if err := hyperv1.AddToScheme(scheme); err != nil {
		t.Fatalf("Failed to add hyperv1 scheme: %v", err)
	}
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatalf("Failed to add corev1 scheme: %v", err)
	}
	if err := configv1.AddToScheme(scheme); err != nil {
		t.Fatalf("Failed to add configv1 scheme: %v", err)
	}
	if err := networkingv1.AddToScheme(scheme); err != nil {
		t.Fatalf("Failed to add networkingv1 scheme: %v", err)
	}

	objs := []client.Object{kubernetesEndpoint, managementClusterNetwork}
	fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(objs...).Build()

	reconciler := &HostedClusterReconciler{
		Client:                        fakeClient,
		ManagementClusterCapabilities: fakecapabilities.NewSupportAllExcept(),
	}

	// Track created network policies
	createdNetworkPolicies := make(map[string]*networkingv1.NetworkPolicy)
	createOrUpdate := upsert.CreateOrUpdateFN(func(ctx context.Context, client client.Client, obj client.Object, f controllerutil.MutateFn) (controllerutil.OperationResult, error) {
		if netPol, ok := obj.(*networkingv1.NetworkPolicy); ok {
			if err := f(); err != nil {
				return controllerutil.OperationResultNone, err
			}
			createdNetworkPolicies[netPol.Name] = netPol
		}
		return controllerutil.OperationResultCreated, nil
	})

	// Execute the test
	ctx := context.Background()
	log := ctrl.Log.WithName("test-azure")
	version := semver.MustParse("4.15.0")

	// Pass true for controlPlaneOperatorAppliesManagementKASNetworkPolicyLabel to test management-kas policy
	err := reconciler.reconcileNetworkPolicies(ctx, log, createOrUpdate, hcluster, hcp, version, true)
	if err != nil {
		t.Fatalf("reconcileNetworkPolicies failed: %v", err)
	}

	// Verify core policies are created for Azure
	expectedPolicies := []string{"openshift-ingress", "same-namespace", "kas", "openshift-monitoring"}
	for _, policyName := range expectedPolicies {
		if _, exists := createdNetworkPolicies[policyName]; !exists {
			t.Errorf("Expected %s NetworkPolicy to be created for Azure", policyName)
		}
	}

	// Verify management-kas NetworkPolicy is created (key difference from before when only AWS was supported)
	if _, exists := createdNetworkPolicies["management-kas"]; !exists {
		t.Error("Expected management-kas NetworkPolicy to be created for Azure platform")
	}

	// Verify the management-kas policy has Azure CSI exclusions
	managementKasPolicy := createdNetworkPolicies["management-kas"]
	if managementKasPolicy != nil {
		var nameExclusion *metav1.LabelSelectorRequirement
		for i := range managementKasPolicy.Spec.PodSelector.MatchExpressions {
			if managementKasPolicy.Spec.PodSelector.MatchExpressions[i].Key == "name" {
				nameExclusion = &managementKasPolicy.Spec.PodSelector.MatchExpressions[i]
				break
			}
		}
		if nameExclusion == nil {
			t.Error("Expected 'name' match expression for Azure CSI exclusions in management-kas policy")
		} else {
			expectedExclusions := []string{"azure-disk-csi-driver-operator", "azure-file-csi-driver-operator"}
			for _, expected := range expectedExclusions {
				found := false
				for _, actual := range nameExclusion.Values {
					if actual == expected {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("Expected Azure CSI exclusion %q not found in management-kas policy", expected)
				}
			}
		}
	}
}

func TestReconcileManagementKASNetworkPolicy_AzureCSIExclusions(t *testing.T) {
	kubernetesEndpoint := &corev1.Endpoints{
		Subsets: []corev1.EndpointSubset{
			{
				Addresses: []corev1.EndpointAddress{
					{IP: "10.0.0.1"},
				},
			},
		},
	}

	managementClusterNetwork := &configv1.Network{
		Spec: configv1.NetworkSpec{
			ClusterNetwork: []configv1.ClusterNetworkEntry{
				{CIDR: "10.128.0.0/14"},
			},
		},
	}

	policy := networkpolicy.ManagementKASNetworkPolicy("test-namespace")

	// Test Azure platform - should exclude Azure CSI operators
	err := reconcileManagementKASNetworkPolicy(policy, managementClusterNetwork, kubernetesEndpoint, true, hyperv1.AzurePlatform, "")
	if err != nil {
		t.Fatalf("reconcileManagementKASNetworkPolicy for Azure failed: %v", err)
	}

	// Verify pod selector has Azure CSI exclusions
	if len(policy.Spec.PodSelector.MatchExpressions) != 2 {
		t.Fatalf("Expected 2 match expressions, got %d", len(policy.Spec.PodSelector.MatchExpressions))
	}

	var nameExclusion *metav1.LabelSelectorRequirement
	for i := range policy.Spec.PodSelector.MatchExpressions {
		if policy.Spec.PodSelector.MatchExpressions[i].Key == "name" {
			nameExclusion = &policy.Spec.PodSelector.MatchExpressions[i]
			break
		}
	}

	if nameExclusion == nil {
		t.Fatal("Expected 'name' match expression for CSI exclusions")
	}

	if nameExclusion.Operator != "NotIn" {
		t.Errorf("Expected NotIn operator, got %s", nameExclusion.Operator)
	}

	expectedExclusions := []string{"azure-disk-csi-driver-operator", "azure-file-csi-driver-operator"}
	if len(nameExclusion.Values) != len(expectedExclusions) {
		t.Errorf("Expected %d exclusions, got %d", len(expectedExclusions), len(nameExclusion.Values))
	}

	for _, expected := range expectedExclusions {
		found := false
		for _, actual := range nameExclusion.Values {
			if actual == expected {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("Expected Azure CSI exclusion %q not found", expected)
		}
	}
}

func TestReconcileManagementKASNetworkPolicy_AWSCSIExclusions(t *testing.T) {
	kubernetesEndpoint := &corev1.Endpoints{
		Subsets: []corev1.EndpointSubset{
			{
				Addresses: []corev1.EndpointAddress{
					{IP: "10.0.0.1"},
				},
			},
		},
	}

	managementClusterNetwork := &configv1.Network{
		Spec: configv1.NetworkSpec{
			ClusterNetwork: []configv1.ClusterNetworkEntry{
				{CIDR: "10.128.0.0/14"},
			},
		},
	}

	policy := networkpolicy.ManagementKASNetworkPolicy("test-namespace")

	// Test AWS platform - should exclude AWS CSI operators
	err := reconcileManagementKASNetworkPolicy(policy, managementClusterNetwork, kubernetesEndpoint, true, hyperv1.AWSPlatform, "")
	if err != nil {
		t.Fatalf("reconcileManagementKASNetworkPolicy for AWS failed: %v", err)
	}

	var nameExclusion *metav1.LabelSelectorRequirement
	for i := range policy.Spec.PodSelector.MatchExpressions {
		if policy.Spec.PodSelector.MatchExpressions[i].Key == "name" {
			nameExclusion = &policy.Spec.PodSelector.MatchExpressions[i]
			break
		}
	}

	if nameExclusion == nil {
		t.Fatal("Expected 'name' match expression for CSI exclusions")
	}

	expectedExclusions := []string{"aws-ebs-csi-driver-operator"}
	if len(nameExclusion.Values) != len(expectedExclusions) {
		t.Errorf("Expected %d exclusions for AWS, got %d", len(expectedExclusions), len(nameExclusion.Values))
	}

	if len(nameExclusion.Values) > 0 && nameExclusion.Values[0] != "aws-ebs-csi-driver-operator" {
		t.Errorf("Expected aws-ebs-csi-driver-operator exclusion, got %q", nameExclusion.Values[0])
	}
}

func TestReconcileManagementKASNetworkPolicy_PodCIDRFallback(t *testing.T) {
	kubernetesEndpoint := &corev1.Endpoints{
		Subsets: []corev1.EndpointSubset{
			{
				Addresses: []corev1.EndpointAddress{
					{IP: "10.0.0.1"},
				},
			},
		},
	}

	testCases := []struct {
		name                     string
		managementClusterNetwork *configv1.Network
		managementClusterPodCIDR string
		expectedExceptions       int // Number of expected CIDR exceptions (KAS IPs + cluster networks)
		expectClusterCIDR        bool
	}{
		{
			name: "When configv1.Network exists it should use cluster network from it",
			managementClusterNetwork: &configv1.Network{
				Spec: configv1.NetworkSpec{
					ClusterNetwork: []configv1.ClusterNetworkEntry{
						{CIDR: "10.128.0.0/14"},
					},
				},
			},
			managementClusterPodCIDR: "",
			expectedExceptions:       2, // 1 KAS IP + 1 cluster network
			expectClusterCIDR:        true,
		},
		{
			name:                     "When configv1.Network is nil it should use managementClusterPodCIDR",
			managementClusterNetwork: nil,
			managementClusterPodCIDR: "10.244.0.0/16",
			expectedExceptions:       2, // 1 KAS IP + 1 pod CIDR
			expectClusterCIDR:        true,
		},
		{
			name:                     "When both are nil it should only block KAS IPs",
			managementClusterNetwork: nil,
			managementClusterPodCIDR: "",
			expectedExceptions:       1, // Only 1 KAS IP
			expectClusterCIDR:        false,
		},
		{
			name: "When both exist it should prefer configv1.Network",
			managementClusterNetwork: &configv1.Network{
				Spec: configv1.NetworkSpec{
					ClusterNetwork: []configv1.ClusterNetworkEntry{
						{CIDR: "10.128.0.0/14"},
					},
				},
			},
			managementClusterPodCIDR: "10.244.0.0/16", // This should be ignored
			expectedExceptions:       2,               // 1 KAS IP + 1 cluster network from configv1.Network
			expectClusterCIDR:        true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			policy := networkpolicy.ManagementKASNetworkPolicy("test-namespace")

			err := reconcileManagementKASNetworkPolicy(policy, tc.managementClusterNetwork, kubernetesEndpoint, true, hyperv1.AzurePlatform, tc.managementClusterPodCIDR)
			if err != nil {
				t.Fatalf("reconcileManagementKASNetworkPolicy failed: %v", err)
			}

			// Find the egress rule with IPBlock (the deny rule)
			var ipBlockRule *networkingv1.NetworkPolicyEgressRule
			for i := range policy.Spec.Egress {
				for _, to := range policy.Spec.Egress[i].To {
					if to.IPBlock != nil {
						ipBlockRule = &policy.Spec.Egress[i]
						break
					}
				}
				if ipBlockRule != nil {
					break
				}
			}

			if ipBlockRule == nil {
				t.Fatal("Expected egress rule with IPBlock")
			}

			// Verify the number of exceptions
			var ipBlock *networkingv1.IPBlock
			for _, to := range ipBlockRule.To {
				if to.IPBlock != nil {
					ipBlock = to.IPBlock
					break
				}
			}

			if ipBlock == nil {
				t.Fatal("Expected IPBlock in egress rule")
			}

			if len(ipBlock.Except) != tc.expectedExceptions {
				t.Errorf("Expected %d exceptions, got %d: %v", tc.expectedExceptions, len(ipBlock.Except), ipBlock.Except)
			}

			// Verify KAS IP is always present
			kasIPFound := false
			for _, cidr := range ipBlock.Except {
				if cidr == "10.0.0.1/32" {
					kasIPFound = true
					break
				}
			}
			if !kasIPFound {
				t.Error("Expected KAS IP 10.0.0.1/32 in exceptions")
			}
		})
	}
}

func TestReconcilePrivateRouterNetworkPolicy_PodCIDRFallback(t *testing.T) {
	hcluster := &hyperv1.HostedCluster{
		Spec: hyperv1.HostedClusterSpec{
			Platform: hyperv1.PlatformSpec{
				Type: hyperv1.AzurePlatform,
			},
		},
	}

	kubernetesEndpoint := &corev1.Endpoints{
		Subsets: []corev1.EndpointSubset{
			{
				Addresses: []corev1.EndpointAddress{
					{IP: "10.0.0.1"},
				},
			},
		},
	}

	testCases := []struct {
		name                     string
		managementClusterNetwork *configv1.Network
		managementClusterPodCIDR string
		expectClusterCIDR        bool
	}{
		{
			name:                     "When configv1.Network is nil it should use managementClusterPodCIDR",
			managementClusterNetwork: nil,
			managementClusterPodCIDR: "10.244.0.0/16",
			expectClusterCIDR:        true,
		},
		{
			name:                     "When both are nil it should only block KAS IPs",
			managementClusterNetwork: nil,
			managementClusterPodCIDR: "",
			expectClusterCIDR:        false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			policy := networkpolicy.PrivateRouterNetworkPolicy("test-namespace")

			err := reconcilePrivateRouterNetworkPolicy(policy, hcluster, kubernetesEndpoint, true, tc.managementClusterNetwork, false, tc.managementClusterPodCIDR)
			if err != nil {
				t.Fatalf("reconcilePrivateRouterNetworkPolicy failed: %v", err)
			}

			// Find the egress rule with IPBlock
			var ipBlock *networkingv1.IPBlock
			for _, egressRule := range policy.Spec.Egress {
				for _, to := range egressRule.To {
					if to.IPBlock != nil {
						ipBlock = to.IPBlock
						break
					}
				}
				if ipBlock != nil {
					break
				}
			}

			if ipBlock == nil {
				t.Fatal("Expected egress rule with IPBlock")
			}

			// Check if cluster CIDR is present based on expectation
			clusterCIDRFound := false
			for _, cidr := range ipBlock.Except {
				if cidr == "10.244.0.0/16" {
					clusterCIDRFound = true
					break
				}
			}

			if tc.expectClusterCIDR && !clusterCIDRFound {
				t.Error("Expected cluster CIDR 10.244.0.0/16 in exceptions")
			}
			if !tc.expectClusterCIDR && clusterCIDRFound {
				t.Error("Did not expect cluster CIDR in exceptions")
			}
		})
	}
}
