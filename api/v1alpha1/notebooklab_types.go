/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package v1alpha1

import (
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

// EDIT THIS FILE!  THIS IS SCAFFOLDING FOR YOU TO OWN!
// NOTE: json tags are required.  Any new fields you add must have json tags for the fields to be serialized.

// NotebookLabSpec defines the desired state of NotebookLab
type NotebookLabSpec struct {
	// Replicas defines the desired number of instances. Used for Pause/Resume. Defaults to 1.
	// +kubebuilder:default:=1
	// +optional
	Replicas *int32 `json:"replicas,omitempty"`

	// Image specifies the Docker image for the Jupyter Notebook.
	Image string `json:"image"`

	// Lifecycle defines the automated resource reclamation policy.
	// +optional
	Lifecycle *LifecycleSpec `json:"lifecycle,omitempty"`

	// ImagePullSecrets specifies the secrets to use for pulling private images.
	// +optional
	ImagePullSecrets []corev1.LocalObjectReference `json:"imagePullSecrets,omitempty"`

	// Resources defines the compute resources required by this notebook.
	// +optional
	Resources corev1.ResourceRequirements `json:"resources,omitempty"`

	// NodeSelector allows requesting specific hardware types or node groups.
	// +optional
	NodeSelector map[string]string `json:"nodeSelector,omitempty"`

	// Tolerations allows the notebook to be scheduled on nodes with matching taints.
	// +optional
	Tolerations []corev1.Toleration `json:"tolerations,omitempty"`

	// GPU defines the GPU configuration and scheduling parameters.
	// +optional
	GPU *GPUSpec `json:"gpu,omitempty"`

	// Storage defines the persistent storage requirements.
	// +optional
	Storage *StorageSpec `json:"storage,omitempty"`
}

type LifecycleSpec struct {
	// IdleTimeoutMinutes defines how long the notebook can be idle before replicas is set to 0.
	// +optional
	IdleTimeoutMinutes int32 `json:"idleTimeoutMinutes,omitempty"`

	// MaxLifespanHours defines the absolute maximum running time before termination.
	// +optional
	MaxLifespanHours int32 `json:"maxLifespanHours,omitempty"`

	// PurgeAfterInactiveDays specifies consecutive days of inactivity before permanently purging all resources including workspace PVC.
	// +optional
	PurgeAfterInactiveDays int32 `json:"purgeAfterInactiveDays,omitempty"`
}

type GPUSpec struct {
	// Enable specifies whether GPU scheduling should be requested.
	// +kubebuilder:default:=false
	// +optional
	Enable bool `json:"enable,omitempty"`

	// Type specifies the GPU provisioner (e.g., hami, mig, standard).
	// +optional
	Type string `json:"type,omitempty"`

	// HAMi contains configuration specific to HAMi vGPU.
	// +optional
	HAMi *HAMiSpec `json:"hami,omitempty"`

	// MIG contains configuration specific to NVIDIA MIG.
	// +optional
	MIG *MIGSpec `json:"mig,omitempty"`
}

type HAMiSpec struct {
	// Cores specifies the percentage or absolute number of GPU cores requested.
	// +optional
	Cores int32 `json:"cores,omitempty"`

	// Memory specifies the amount of vGPU memory requested.
	// +optional
	Memory resource.Quantity `json:"memory,omitempty"`
}

type MIGSpec struct {
	// Profile specifies the MIG profile to request (e.g., 1g.5gb).
	// +optional
	Profile string `json:"profile,omitempty"`
}

type StorageSpec struct {
	// Workspace defines the personal Read-Write storage.
	// +optional
	Workspace *WorkspaceSpec `json:"workspace,omitempty"`

	// Datasets defines a list of shared Read-Only datasets.
	// +optional
	Datasets []DatasetSpec `json:"datasets,omitempty"`

	// Tmp defines the temporary emptyDir storage for runtime scratch files.
	// +optional
	Tmp *TmpSpec `json:"tmp,omitempty"`
}

type WorkspaceSpec struct {
	// Size specifies the capacity of the workspace PVC.
	Size resource.Quantity `json:"size"`

	// StorageClassName specifies the provisioner to use.
	// +optional
	StorageClassName string `json:"storageClassName,omitempty"`

	// MountPath specifies where to mount the workspace inside the notebook container.
	// +kubebuilder:default:="/workspace"
	// +optional
	MountPath string `json:"mountPath,omitempty"`
}

type TmpSpec struct {
	// SizeLimit specifies the capacity limit of the temporary /tmp emptyDir volume.
	// +optional
	SizeLimit resource.Quantity `json:"sizeLimit,omitempty"`

	// MountPath specifies where to mount the temporary volume inside the container. Defaults to "/tmp".
	// +kubebuilder:default:="/tmp"
	// +optional
	MountPath string `json:"mountPath,omitempty"`
}

type DatasetSpec struct {
	// Name is the display name of the dataset.
	Name string `json:"name"`

	// PVCName is the name of the existing shared PVC on the cluster.
	PVCName string `json:"pvcName"`

	// MountPath specifies where to mount the dataset inside the notebook container.
	MountPath string `json:"mountPath"`
}

// NotebookLabPhase defines the overall status of the notebook
type NotebookLabPhase string

const (
	PhaseProvisioning NotebookLabPhase = "Provisioning"
	PhaseRunning      NotebookLabPhase = "Running"
	PhasePausing      NotebookLabPhase = "Pausing"
	PhasePaused       NotebookLabPhase = "Paused"
	PhaseFailed       NotebookLabPhase = "Failed"
)

// NotebookLabStatus defines the observed state of NotebookLab.
type NotebookLabStatus struct {
	// Phase is the high-level status of the NotebookLab.
	// +optional
	Phase NotebookLabPhase `json:"phase,omitempty"`

	// PodName is the name of the underlying active Pod.
	// +optional
	PodName string `json:"podName,omitempty"`

	// PVCName is the name of the provisioned workspace PVC.
	// +optional
	PVCName string `json:"pvcName,omitempty"`

	// AccessURL is the URL where the user can access the JupyterLab interface.
	// +optional
	AccessURL string `json:"accessUrl,omitempty"`

	// LastActiveTime records the last time interaction was detected.
	// +optional
	LastActiveTime *metav1.Time `json:"lastActiveTime,omitempty"`

	// ExpiresAt records when the notebook is scheduled for auto-termination.
	// +optional
	ExpiresAt *metav1.Time `json:"expiresAt,omitempty"`

	// NodeName is the name of the node where the pod is currently running.
	// +optional
	NodeName string `json:"nodeName,omitempty"`

	// GPUStatus reports the actual GPU resources allocated.
	// +optional
	GPUStatus *GPUStatus `json:"gpuStatus,omitempty"`

	// Conditions represent the current state of the NotebookLab resource.
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

type GPUStatus struct {
	// Allocated indicates if a GPU was successfully attached.
	// +optional
	Allocated bool `json:"allocated,omitempty"`

	// Type indicates the provisioning technology used.
	// +optional
	Type string `json:"type,omitempty"`

	// CoresAllocated indicates the actual cores given.
	// +optional
	CoresAllocated int32 `json:"coresAllocated,omitempty"`

	// MemoryAllocated indicates the actual memory given.
	// +optional
	MemoryAllocated string `json:"memoryAllocated,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Phase",type="string",JSONPath=".status.phase",description="The current phase of the NotebookLab"
// +kubebuilder:printcolumn:name="Pod",type="string",JSONPath=".status.podName",description="The active Pod name"
// +kubebuilder:printcolumn:name="URL",type="string",JSONPath=".status.accessUrl",description="The Access URL"
// +kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp"

// NotebookLab is the Schema for the notebooklabs API
type NotebookLab struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitempty"`

	// spec defines the desired state of NotebookLab
	// +required
	Spec NotebookLabSpec `json:"spec"`

	// status defines the observed state of NotebookLab
	// +optional
	Status NotebookLabStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// NotebookLabList contains a list of NotebookLab
type NotebookLabList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []NotebookLab `json:"items"`
}

func init() {
	SchemeBuilder.Register(func(s *runtime.Scheme) error {
		s.AddKnownTypes(SchemeGroupVersion, &NotebookLab{}, &NotebookLabList{})
		return nil
	})
}
