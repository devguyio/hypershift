package storage

import (
	"testing"

	operatorv1 "github.com/openshift/api/operator/v1"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestReconcileClusterCSIDriverKMSKey(t *testing.T) {
	tests := []struct {
		name               string
		driver             *operatorv1.ClusterCSIDriver
		kmsKeyARN          string
		expectDriverConfig bool
		expectedARN        string
	}{
		{
			name: "new driver with KMS key sets DriverConfig",
			driver: &operatorv1.ClusterCSIDriver{
				ObjectMeta: metav1.ObjectMeta{
					Name: string(operatorv1.AWSEBSCSIDriver),
				},
			},
			kmsKeyARN:          "arn:aws:kms:us-east-1:123456789012:key/mrk-abc123",
			expectDriverConfig: true,
			expectedARN:        "arn:aws:kms:us-east-1:123456789012:key/mrk-abc123",
		},
		{
			name: "existing driver with empty DriverConfig gets KMS key set",
			driver: &operatorv1.ClusterCSIDriver{
				ObjectMeta: metav1.ObjectMeta{
					Name:            string(operatorv1.AWSEBSCSIDriver),
					ResourceVersion: "12345",
				},
			},
			kmsKeyARN:          "arn:aws:kms:us-east-1:123456789012:key/mrk-abc123",
			expectDriverConfig: true,
			expectedARN:        "arn:aws:kms:us-east-1:123456789012:key/mrk-abc123",
		},
		{
			name: "existing driver with KMS key already set is NOT overwritten",
			driver: &operatorv1.ClusterCSIDriver{
				ObjectMeta: metav1.ObjectMeta{
					Name:            string(operatorv1.AWSEBSCSIDriver),
					ResourceVersion: "12345",
				},
				Spec: operatorv1.ClusterCSIDriverSpec{
					DriverConfig: operatorv1.CSIDriverConfigSpec{
						DriverType: operatorv1.AWSDriverType,
						AWS: &operatorv1.AWSCSIDriverConfigSpec{
							KMSKeyARN: "arn:aws:kms:us-east-1:123456789012:key/original-key",
						},
					},
				},
			},
			kmsKeyARN:          "arn:aws:kms:us-east-1:123456789012:key/new-key",
			expectDriverConfig: true,
			expectedARN:        "arn:aws:kms:us-east-1:123456789012:key/original-key",
		},
		{
			name: "new driver with empty KMS key does not set DriverConfig",
			driver: &operatorv1.ClusterCSIDriver{
				ObjectMeta: metav1.ObjectMeta{
					Name: string(operatorv1.AWSEBSCSIDriver),
				},
			},
			kmsKeyARN:          "",
			expectDriverConfig: false,
		},
		{
			name: "existing driver with empty KMS key preserves existing DriverConfig",
			driver: &operatorv1.ClusterCSIDriver{
				ObjectMeta: metav1.ObjectMeta{
					Name:            string(operatorv1.AWSEBSCSIDriver),
					ResourceVersion: "12345",
				},
				Spec: operatorv1.ClusterCSIDriverSpec{
					DriverConfig: operatorv1.CSIDriverConfigSpec{
						DriverType: operatorv1.AWSDriverType,
						AWS: &operatorv1.AWSCSIDriverConfigSpec{
							KMSKeyARN: "arn:aws:kms:us-east-1:123456789012:key/existing-key",
						},
					},
				},
			},
			kmsKeyARN:          "",
			expectDriverConfig: true,
			expectedARN:        "arn:aws:kms:us-east-1:123456789012:key/existing-key",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ReconcileClusterCSIDriverKMSKey(tt.driver, tt.kmsKeyARN)

			if tt.expectDriverConfig {
				if tt.driver.Spec.DriverConfig.AWS == nil {
					t.Fatalf("expected DriverConfig.AWS to be set, got nil")
				}
				if tt.driver.Spec.DriverConfig.AWS.KMSKeyARN != tt.expectedARN {
					t.Errorf("expected KMSKeyARN %q, got %q", tt.expectedARN, tt.driver.Spec.DriverConfig.AWS.KMSKeyARN)
				}
			} else {
				if tt.driver.Spec.DriverConfig.AWS != nil {
					t.Errorf("expected DriverConfig.AWS to be nil, got %+v", tt.driver.Spec.DriverConfig.AWS)
				}
			}
		})
	}
}
