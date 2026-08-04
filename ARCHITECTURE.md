# Jupiter Operator Architecture
## 1. API
- Định nghĩa VirtualNotebook:
```yaml
apiVersion: lab.ngtukien.id.vn/v1alpha1
kind: VirtualNotebook
metadata:
  name: virtualnotebook-sample
  labels:
    UserID: "1"
    ProjectID: "1"
spec:
  # Số lượng bản sao (Dùng để Tạm dừng / Tiếp tục). 
  # MẶC ĐỊNH LÀ 1. User KHÔNG CẦN khai báo dòng này khi tạo mới.
  # Tự thu hồi theo lifecycle
  # replicas: 1 
  image: "jupyter/scipy-notebook:latest"
  
  # [MỚI] Cấu hình vòng đời và thu hồi tự động
  lifecycle:
    idleTimeoutMinutes: 60    # Sau 60p không chạy code -> tự động set replicas = 0
    maxLifespanHours: 12      # Hard limit: Ép tắt sau 12h dù có đang chạy hay không
    
  # Hỗ trợ kéo Image từ Private Registry (Nếu có)
  imagePullSecrets:
    - name: "my-private-registry-secret"

  
  # [CẢI TIẾN] Chuẩn hóa theo corev1.ResourceRequirements của K8s
  resources:
    requests:
      cpu: "2"
      memory: "2Gi"
    limits:
      cpu: "4"
      memory: "4Gi"
      
  gpu: 
    enable: true
    type: "hami" 
    
    # [MỚI] Chọn loại GPU phần cứng muốn chạy
    nodeSelector:
      gpu-type: "a100"

    hami:
      cores: 20       
      memory: "16Gi" 
      
    # [MỚI] Nếu type là "mig", cấu hình sẽ khai báo qua profile phần cứng (Comment lại để tham khảo)
    # mig:
    #   profile: "1g.5gb" # Map thành resource limit: nvidia.com/mig-1g.5gb: 1

  # [CẢI TIẾN] Hỗ trợ cấu hình lưu trữ đa dạng hơn
  storage:
    # Workspace cá nhân (Read-Write) - Giữ lại khi replicas = 0
    workspace:
      size: "10Gi"
      storageClassName: "local-path" # Thay vì type "local", trỏ thẳng vào SC của K8s
      mountPath: "/workspace"
    
    # [MỚI] Danh sách các Dataset dùng chung (Read-only)
    datasets:
      - name: "imagenet-2012"
        pvcName: "shared-imagenet-pvc" # PVC chứa data dùng chung đã có sẵn trên cụm
        mountPath: "/datasets/imagenet"
```

- Trạng thái trả về (Status):
```yaml
status:
  # Trạng thái tổng thể của Notebook (Dựa trên Spec.Replicas và thực tế K8s)
  # - Provisioning: Đang cấp phát PVC/Pod/GPU
  # - Running: Pod đang chạy, GPU đã gắn, đã có URL
  # - Pausing: Đang trong quá trình tắt Pod để thu hồi tài nguyên
  # - Paused: Pod đã tắt, GPU đã thu hồi, PVC vẫn còn giữ
  # - Failed: Lỗi cấp phát (Hết tài nguyên, sai cấu hình, v.v.)
  phase: "Running"

  # Tên Pod thực tế dưới Kubernetes (để dễ debug)
  podName: "virtualnotebook-sample-pod"

  # Tên ổ cứng đã cấp phát thực tế (PVC)
  pvcName: "virtualnotebook-sample-workspace-pvc"

  # Đường dẫn (URL) để sinh viên bấm vào mở giao diện JupyterLab lập tức
  # Operator tự sinh ra chuỗi này khi tạo Ingress
  accessUrl: "https://virtualnotebook-sample.lab.ngtukien.id.vn"

  # [MỚI] Thông tin quản lý vòng đời (để hiển thị Countdown UI)
  lastActiveTime: "2026-08-04T12:00:00Z" # Lần cuối ghi nhận có tương tác
  expiresAt: "2026-08-04T13:00:00Z"      # Thời điểm dự kiến tắt máy do Idle hoặc hết Lifespan

  # [MỚI] Thông tin Node đang chạy
  nodeName: "gpu-worker-01"

  # Báo cáo tình trạng GPU thực tế
  gpuStatus:
    allocated: true
    type: "hami"
    coresAllocated: 20
    memoryAllocated: "16Gi"

  # Trạng thái chuẩn của Kubernetes (K8s Conditions)
  # Chứa lỗi chi tiết nếu Phase == Failed
  conditions:
    - type: "Ready"
      status: "True"
      lastTransitionTime: "2026-08-04T12:00:00Z"
      reason: "PodRunningAndIngressReady"
      message: "Notebook is ready to accept connections"
```
