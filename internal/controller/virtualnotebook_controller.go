/*
Copyright 2026.
*/

package controller

import (
	"context"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	resourcev1 "k8s.io/api/resource/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	labv1alpha1 "ngtukien/jupiter-operator/api/v1alpha1"
)

const appLabelKey = "app"

// VirtualNotebookReconciler reconciles a VirtualNotebook object
type VirtualNotebookReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// Cấp quyền (RBAC) cho Operator để nó có thể thao tác với các Resource của K8s
// +kubebuilder:rbac:groups=lab.ngtukien.id.vn,resources=virtualnotebooks,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=lab.ngtukien.id.vn,resources=virtualnotebooks/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=lab.ngtukien.id.vn,resources=virtualnotebooks/finalizers,verbs=update
// +kubebuilder:rbac:groups="",resources=persistentvolumeclaims;services;secrets;events,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=apps,resources=deployments,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=networking.k8s.io,resources=ingresses,verbs=get;list;watch;create;update;patch;delete

func (r *VirtualNotebookReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	// =========================================================================
	// 1. Fetch instance VirtualNotebook
	// =========================================================================
	notebook := &labv1alpha1.VirtualNotebook{}
	err := r.Get(ctx, req.NamespacedName, notebook)
	if err != nil {
		if apierrors.IsNotFound(err) {
			// CRD đã bị xóa, K8s Garbage Collector sẽ tự lo phần dọn dẹp nếu đã set OwnerReference
			return ctrl.Result{}, nil
		}
		logger.Error(err, "Failed to get VirtualNotebook")
		return ctrl.Result{}, err
	}

	// =========================================================================
	// 2. Reconcile PVC (Workspace)
	// Đảm bảo ổ cứng luôn tồn tại bất kể replicas là 0 hay 1
	// =========================================================================
	if notebook.Spec.Storage != nil && notebook.Spec.Storage.Workspace != nil {
		err = r.reconcileWorkspacePVC(ctx, notebook)
		if err != nil {
			return r.updateStatusError(ctx, notebook, "Failed to reconcile PVC", err)
		}
	}

	// =========================================================================
	// 3. Reconcile Deployment & Secret (Token)
	// Quản lý việc Tạm dừng / Tiếp tục bằng cách scale Deployment về 0 hoặc 1
	// =========================================================================
	err = r.reconcileJupyterDeployment(ctx, notebook)
	if err != nil {
		return r.updateStatusError(ctx, notebook, "Failed to reconcile Deployment", err)
	}

	// =========================================================================
	// 4. Reconcile Service & Ingress (Network)
	// Mở kết nối mạng từ bên ngoài vào JupyterLab
	// =========================================================================
	err = r.reconcileNetworking(ctx, notebook)
	if err != nil {
		return r.updateStatusError(ctx, notebook, "Failed to reconcile Networking", err)
	}

	// =========================================================================
	// 5. Cập nhật Status
	// Đọc trạng thái thực tế của Deployment/Ingress và báo cáo về CRD
	// =========================================================================
	err = r.updateNotebookStatus(ctx, notebook)
	if err != nil {
		logger.Error(err, "Failed to update VirtualNotebook status")
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

// SetupWithManager thiết lập theo dõi các tài nguyên liên quan
func (r *VirtualNotebookReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&labv1alpha1.VirtualNotebook{}).
		// Tell Manager to watch these resources owned by VirtualNotebook
		Owns(&appsv1.Deployment{}).
		Owns(&corev1.PersistentVolumeClaim{}).
		Owns(&corev1.Service{}).
		Owns(&networkingv1.Ingress{}).
		Owns(&corev1.Secret{}).                    // Thêm Watch Secret
		Owns(&resourcev1.ResourceClaimTemplate{}). // Thêm Watch DRA Template
		Complete(r)
}

// --- Các hàm Helper (Bạn sẽ implement chi tiết các hàm này) ---

func (r *VirtualNotebookReconciler) reconcileWorkspacePVC(ctx context.Context, notebook *labv1alpha1.VirtualNotebook) error {
	logger := log.FromContext(ctx)

	// Quy ước tên PVC sẽ là: <tên-notebook>-workspace
	pvcName := notebook.Name + "-workspace"
	pvc := &corev1.PersistentVolumeClaim{}

	// Kiểm tra xem PVC đã tồn tại trên K8s chưa
	err := r.Get(ctx, types.NamespacedName{Name: pvcName, Namespace: notebook.Namespace}, pvc)

	if err != nil && apierrors.IsNotFound(err) {
		// Nếu chưa tồn tại -> Tiến hành tạo mới
		logger.Info("Creating a new Workspace PVC", "PVC.Namespace", notebook.Namespace, "PVC.Name", pvcName)

		// Khởi tạo đối tượng PVC
		newPVC := &corev1.PersistentVolumeClaim{
			ObjectMeta: metav1.ObjectMeta{
				Name:      pvcName,
				Namespace: notebook.Namespace,
				Labels:    notebook.Labels, // Kế thừa label từ CRD xuống để dễ quản lý
			},
			Spec: corev1.PersistentVolumeClaimSpec{
				AccessModes: []corev1.PersistentVolumeAccessMode{
					corev1.ReadWriteOnce, // Jupyter thường chỉ cần RWO
				},
				Resources: corev1.VolumeResourceRequirements{
					Requests: corev1.ResourceList{
						corev1.ResourceStorage: notebook.Spec.Storage.Workspace.Size,
					},
				},
			},
		}

		// Nếu user có chỉ định StorageClass thì map vào
		if notebook.Spec.Storage.Workspace.StorageClassName != "" {
			newPVC.Spec.StorageClassName = &notebook.Spec.Storage.Workspace.StorageClassName
		}

		// BƯỚC QUAN TRỌNG NHẤT: Gắn Owner Reference
		// Khi user xóa VirtualNotebook CRD, K8s sẽ tự động xóa PVC này (Garbage Collection)
		if err := ctrl.SetControllerReference(notebook, newPVC, r.Scheme); err != nil {
			return err
		}

		// Đẩy lệnh Create xuống K8s API
		if err := r.Create(ctx, newPVC); err != nil {
			logger.Error(err, "Failed to create new PVC", "PVC.Namespace", newPVC.Namespace, "PVC.Name", newPVC.Name)
			return err
		}

		// Tạo thành công, return nil để đi tiếp tới bước Deployment
		return nil
	} else if err != nil {
		// Gặp lỗi khác (ví dụ: mất kết nối DB K8s)
		return err
	}

	// Nếu code chạy đến đây nghĩa là PVC đã tồn tại (không bị lỗi IsNotFound)
	// Ta không cần làm gì thêm, để nguyên PVC cũ giữ an toàn dữ liệu
	logger.Info("Workspace PVC already exists", "PVC.Namespace", notebook.Namespace, "PVC.Name", pvcName)
	return nil
}

func (r *VirtualNotebookReconciler) reconcileNotebookSecret(ctx context.Context, notebook *labv1alpha1.VirtualNotebook) (string, error) {
	secretName := notebook.Name + "-secret"
	secret := &corev1.Secret{}

	err := r.Get(ctx, types.NamespacedName{Name: secretName, Namespace: notebook.Namespace}, secret)
	if err != nil && apierrors.IsNotFound(err) {
		// Sinh token ngẫu nhiên hoặc hash dựa trên Name
		token := notebook.Name + "-token-sec"

		newSecret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      secretName,
				Namespace: notebook.Namespace,
				Labels:    notebook.Labels,
			},
			StringData: map[string]string{
				"token": token,
			},
		}

		if err := ctrl.SetControllerReference(notebook, newSecret, r.Scheme); err != nil {
			return "", err
		}
		if err := r.Create(ctx, newSecret); err != nil {
			return "", err
		}
		return token, nil
	} else if err != nil {
		return "", err
	}

	return string(secret.Data["token"]), nil
}

func (r *VirtualNotebookReconciler) reconcileJupyterDeployment(ctx context.Context, notebook *labv1alpha1.VirtualNotebook) error {
	logger := log.FromContext(ctx)
	deployName := notebook.Name

	// 1. Xác định số Replicas (Pause/Resume logic)
	replicas := int32(1)
	if notebook.Spec.Replicas != nil {
		replicas = *notebook.Spec.Replicas
	}

	// 2. Cấu hình Volumes và VolumeMounts
	volumes := []corev1.Volume{}
	volumeMounts := []corev1.VolumeMount{}

	// Workspace Volume
	if notebook.Spec.Storage != nil && notebook.Spec.Storage.Workspace != nil {
		volumes = append(volumes, corev1.Volume{
			Name: "workspace",
			VolumeSource: corev1.VolumeSource{
				PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
					ClaimName: notebook.Name + "-workspace",
				},
			},
		})
		volumeMounts = append(volumeMounts, corev1.VolumeMount{
			Name:      "workspace",
			MountPath: notebook.Spec.Storage.Workspace.MountPath,
		})
	}

	// Datasets Volumes
	if notebook.Spec.Storage != nil && len(notebook.Spec.Storage.Datasets) > 0 {
		for _, ds := range notebook.Spec.Storage.Datasets {
			volumes = append(volumes, corev1.Volume{
				Name: ds.Name,
				VolumeSource: corev1.VolumeSource{
					PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
						ClaimName: ds.PVCName,
						ReadOnly:  true,
					},
				},
			})
			volumeMounts = append(volumeMounts, corev1.VolumeMount{
				Name:      ds.Name,
				MountPath: ds.MountPath,
				ReadOnly:  true,
			})
		}
	}

	// Mount /tmp emptyDir volume cho các ứng dụng chạy tạm (Jupyter/Python)
	emptyDirSource := &corev1.EmptyDirVolumeSource{}
	tmpMountPath := "/tmp"

	if notebook.Spec.Storage != nil && notebook.Spec.Storage.Tmp != nil {
		if !notebook.Spec.Storage.Tmp.SizeLimit.IsZero() {
			emptyDirSource.SizeLimit = &notebook.Spec.Storage.Tmp.SizeLimit
		}
		if notebook.Spec.Storage.Tmp.MountPath != "" {
			tmpMountPath = notebook.Spec.Storage.Tmp.MountPath
		}
	}

	volumes = append(volumes, corev1.Volume{
		Name: "tmp",
		VolumeSource: corev1.VolumeSource{
			EmptyDir: emptyDirSource,
		},
	})
	volumeMounts = append(volumeMounts, corev1.VolumeMount{
		Name:      "tmp",
		MountPath: tmpMountPath,
	})

	// 3. Xử lý Tolerations tự động (Auto-inject cho GPU)
	tolerations := notebook.Spec.Tolerations
	if notebook.Spec.GPU != nil && notebook.Spec.GPU.Enable && len(tolerations) == 0 {
		// Tự động inject toleration phổ biến cho GPU node
		tolerations = append(tolerations, corev1.Toleration{
			Key:      "nvidia.com/gpu",
			Operator: corev1.TolerationOpExists,
			Effect:   corev1.TaintEffectNoSchedule,
		})
	}

	// Lấy token từ Secret
	_, err := r.reconcileNotebookSecret(ctx, notebook)
	if err != nil {
		return err
	}

	// 4. Cấu hình Bảo mật (Security Context & Non-Root)
	runAsNonRoot := true
	runAsUser := int64(1000)
	runAsGroup := int64(1000)
	fsGroup := int64(1000)
	autoMountToken := false
	allowPrivilegeEscalation := false

	podSecurityContext := &corev1.PodSecurityContext{
		RunAsNonRoot: &runAsNonRoot,
		RunAsUser:    &runAsUser,
		RunAsGroup:   &runAsGroup,
		FSGroup:      &fsGroup,
		SeccompProfile: &corev1.SeccompProfile{
			Type: corev1.SeccompProfileTypeRuntimeDefault,
		},
	}

	containerSecurityContext := &corev1.SecurityContext{
		AllowPrivilegeEscalation: &allowPrivilegeEscalation,
		Capabilities: &corev1.Capabilities{
			Drop: []corev1.Capability{"ALL"},
		},
	}

	// 5. Định nghĩa Pod Spec
	podSpec := corev1.PodSpec{
		AutomountServiceAccountToken: &autoMountToken,
		SecurityContext:              podSecurityContext,
		Containers: []corev1.Container{
			{
				Name:            "jupyter",
				Image:           notebook.Spec.Image,
				Resources:       notebook.Spec.Resources, // Kế thừa chuẩn K8s
				VolumeMounts:    volumeMounts,
				SecurityContext: containerSecurityContext,
				Env: []corev1.EnvVar{
					{
						Name: "JUPYTER_TOKEN",
						ValueFrom: &corev1.EnvVarSource{
							SecretKeyRef: &corev1.SecretKeySelector{
								LocalObjectReference: corev1.LocalObjectReference{Name: notebook.Name + "-secret"},
								Key:                  "token",
							},
						},
					},
				},
			},
		},
		Volumes:          volumes,
		NodeSelector:     notebook.Spec.NodeSelector,
		Tolerations:      tolerations,
		ImagePullSecrets: notebook.Spec.ImagePullSecrets,
	}

	// 5. Cấu hình DRA (Dynamic Resource Allocation) cho GPU
	if notebook.Spec.GPU != nil && notebook.Spec.GPU.Enable {
		// Gọi hàm helper để tạo ResourceClaimTemplate object
		if err := r.reconcileGPUResourceClaimTemplate(ctx, notebook); err != nil {
			return err
		}

		templateName := notebook.Name + "-gpu-template"

		// Map ResourceClaimTemplate vào Pod Spec
		podSpec.ResourceClaims = []corev1.PodResourceClaim{
			{
				Name:                      "gpu-claim",
				ResourceClaimTemplateName: &templateName,
			},
		}
	}

	// 6. Xây dựng đối tượng Deployment
	deploy := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      deployName,
			Namespace: notebook.Namespace,
			Labels:    notebook.Labels,
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{appLabelKey: deployName},
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{appLabelKey: deployName},
				},
				Spec: podSpec,
			},
		},
	}

	// Gắn Owner Reference
	if err := ctrl.SetControllerReference(notebook, deploy, r.Scheme); err != nil {
		return err
	}

	// 7. Apply Deployment (Tạo mới hoặc Cập nhật qua Patch)
	existingDeploy := &appsv1.Deployment{}
	err = r.Get(ctx, types.NamespacedName{Name: deployName, Namespace: notebook.Namespace}, existingDeploy)
	if err != nil && apierrors.IsNotFound(err) {
		logger.Info("Creating a new Jupyter Deployment", "Namespace", deploy.Namespace, "Name", deploy.Name)
		return r.Create(ctx, deploy)
	} else if err == nil {
		// Dùng Patch với MergeFrom để chỉ cập nhật các trường linh hoạt (Replicas, Template)
		// Tránh lỗi API server từ chối do trùng/đổi immutable fields (LabelSelector)
		patch := client.MergeFrom(existingDeploy.DeepCopy())
		existingDeploy.Spec.Replicas = deploy.Spec.Replicas
		existingDeploy.Spec.Template = deploy.Spec.Template
		return r.Patch(ctx, existingDeploy, patch)
	}

	return err
}

func (r *VirtualNotebookReconciler) reconcileGPUResourceClaimTemplate(ctx context.Context, notebook *labv1alpha1.VirtualNotebook) error {
	logger := log.FromContext(ctx)
	templateName := notebook.Name + "-gpu-template"

	template := &resourcev1.ResourceClaimTemplate{}
	err := r.Get(ctx, types.NamespacedName{Name: templateName, Namespace: notebook.Namespace}, template)

	if err != nil && apierrors.IsNotFound(err) {
		logger.Info("Creating a new GPU ResourceClaimTemplate", "Namespace", notebook.Namespace, "Name", templateName)

		deviceClassName := "hami" // Tên DeviceClass mặc định cho HAMi
		if notebook.Spec.GPU.Type != "" {
			deviceClassName = notebook.Spec.GPU.Type
		}

		template = &resourcev1.ResourceClaimTemplate{
			ObjectMeta: metav1.ObjectMeta{
				Name:      templateName,
				Namespace: notebook.Namespace,
				Labels:    notebook.Labels,
			},
			Spec: resourcev1.ResourceClaimTemplateSpec{
				Spec: resourcev1.ResourceClaimSpec{
					Devices: resourcev1.DeviceClaim{
						Requests: []resourcev1.DeviceRequest{
							{
								Name: "gpu",
								Exactly: &resourcev1.ExactDeviceRequest{
									DeviceClassName: deviceClassName,
								},
							},
						},
					},
				},
			},
		}

		if err := ctrl.SetControllerReference(notebook, template, r.Scheme); err != nil {
			return err
		}

		if err := r.Create(ctx, template); err != nil {
			logger.Error(err, "Failed to create GPU ResourceClaimTemplate", "Name", template.Name)
			return err
		}
		return nil
	} else if err != nil {
		return err
	}

	logger.Info("GPU ResourceClaimTemplate already exists", "Namespace", notebook.Namespace, "Name", templateName)
	return nil
}

func (r *VirtualNotebookReconciler) reconcileNetworking(ctx context.Context, notebook *labv1alpha1.VirtualNotebook) error {
	logger := log.FromContext(ctx)

	// 1. Reconcile Service
	svcName := notebook.Name + "-svc"
	svc := &corev1.Service{}
	err := r.Get(ctx, types.NamespacedName{Name: svcName, Namespace: notebook.Namespace}, svc)
	if err != nil && apierrors.IsNotFound(err) {
		svc = &corev1.Service{
			ObjectMeta: metav1.ObjectMeta{
				Name:      svcName,
				Namespace: notebook.Namespace,
				Labels:    notebook.Labels,
			},
			Spec: corev1.ServiceSpec{
				Selector: map[string]string{appLabelKey: notebook.Name},
				Ports: []corev1.ServicePort{
					{
						Name: "http",
						Port: 8888,
					},
				},
			},
		}
		if err := ctrl.SetControllerReference(notebook, svc, r.Scheme); err != nil {
			return err
		}
		logger.Info("Creating Service", "Name", svcName)
		if err := r.Create(ctx, svc); err != nil {
			return err
		}
	} else if err != nil {
		return err
	}

	// 2. Reconcile Ingress
	ingName := notebook.Name + "-ingress"
	ing := &networkingv1.Ingress{}
	err = r.Get(ctx, types.NamespacedName{Name: ingName, Namespace: notebook.Namespace}, ing)
	if err != nil && apierrors.IsNotFound(err) {
		pathType := networkingv1.PathTypePrefix
		host := notebook.Name + ".lab.ngtukien.id.vn" // Domain mặc định theo chuẩn KubeEdu

		ing = &networkingv1.Ingress{
			ObjectMeta: metav1.ObjectMeta{
				Name:      ingName,
				Namespace: notebook.Namespace,
				Labels:    notebook.Labels,
				Annotations: map[string]string{
					"nginx.ingress.kubernetes.io/proxy-read-timeout": "3600",
					"nginx.ingress.kubernetes.io/proxy-send-timeout": "3600",
					"nginx.ingress.kubernetes.io/websocket-services": svcName,
				},
			},
			Spec: networkingv1.IngressSpec{
				Rules: []networkingv1.IngressRule{
					{
						Host: host,
						IngressRuleValue: networkingv1.IngressRuleValue{
							HTTP: &networkingv1.HTTPIngressRuleValue{
								Paths: []networkingv1.HTTPIngressPath{
									{
										Path:     "/",
										PathType: &pathType,
										Backend: networkingv1.IngressBackend{
											Service: &networkingv1.IngressServiceBackend{
												Name: svcName,
												Port: networkingv1.ServiceBackendPort{
													Number: 8888,
												},
											},
										},
									},
								},
							},
						},
					},
				},
			},
		}
		if err := ctrl.SetControllerReference(notebook, ing, r.Scheme); err != nil {
			return err
		}
		logger.Info("Creating Ingress", "Name", ingName)
		if err := r.Create(ctx, ing); err != nil {
			return err
		}
	} else if err != nil {
		return err
	}

	return nil
}

func (r *VirtualNotebookReconciler) updateNotebookStatus(ctx context.Context, notebook *labv1alpha1.VirtualNotebook) error {
	// Fetch actual Deployment
	deploy := &appsv1.Deployment{}
	err := r.Get(ctx, types.NamespacedName{Name: notebook.Name, Namespace: notebook.Namespace}, deploy)
	if err != nil {
		if apierrors.IsNotFound(err) {
			notebook.Status.Phase = labv1alpha1.PhaseProvisioning
			return r.Status().Update(ctx, notebook)
		}
		return err
	}

	// Determine Phase based on Replicas
	replicas := int32(1)
	if notebook.Spec.Replicas != nil {
		replicas = *notebook.Spec.Replicas
	}

	if replicas == 0 {
		if deploy.Status.Replicas == 0 {
			notebook.Status.Phase = labv1alpha1.PhasePaused
		} else {
			notebook.Status.Phase = labv1alpha1.PhasePausing
		}
	} else {
		if deploy.Status.ReadyReplicas > 0 {
			notebook.Status.Phase = labv1alpha1.PhaseRunning
		} else {
			notebook.Status.Phase = labv1alpha1.PhaseProvisioning
		}
	}

	// Update basic info
	token, err := r.reconcileNotebookSecret(ctx, notebook)
	if err != nil {
		token = notebook.Name + "-token-sec"
	}
	notebook.Status.PVCName = notebook.Name + "-workspace"
	notebook.Status.AccessURL = "https://" + notebook.Name + ".lab.ngtukien.id.vn/lab?token=" + token

	// Gọi API update Status
	return r.Status().Update(ctx, notebook)
}

func (r *VirtualNotebookReconciler) updateStatusError(ctx context.Context, notebook *labv1alpha1.VirtualNotebook, msg string, err error) (ctrl.Result, error) {
	// Helper function to set Phase = Failed and log message when an error occurs
	logger := log.FromContext(ctx)
	logger.Error(err, "Reconciliation failed", "message", msg)
	notebook.Status.Phase = labv1alpha1.PhaseFailed
	if updateErr := r.Status().Update(ctx, notebook); updateErr != nil {
		logger.Error(updateErr, "Failed to update VirtualNotebook status on error")
	}
	return ctrl.Result{}, err
}
