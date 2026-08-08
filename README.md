# Notebook Operator

[![Kubernetes](https://img.shields.io/badge/Kubernetes-v1.34+-326ce5?logo=kubernetes&logoColor=white)](https://kubernetes.io)
[![Go](https://img.shields.io/badge/Go-v1.26+-00ADD8?logo=go&logoColor=white)](https://go.dev)
[![License](https://img.shields.io/badge/License-Apache%202.0-blue.svg)](LICENSE)

Kubernetes Operator quản lý vòng đời môi trường JupyterLab trên hạ tầng GPU, phục vụ đào tạo AI/ML và nghiên cứu khoa học dữ liệu.

Operator tự động hóa toàn bộ quy trình: cấp phát workspace, phân bổ GPU (HAMi vGPU / MIG qua Device Plugin), expose notebook qua Ingress, và thu hồi tài nguyên khi idle — thông qua một Custom Resource duy nhất: **`NotebookLab`**.

## Tính Năng Chính

- **GPU Scheduling qua Device Plugin** — Hỗ trợ đồng thời HAMi vGPU sharing (`nvidia.com/gpu` + `nvidia.com/gpumem` + `nvidia.com/gpucores`) và NVIDIA MIG (`nvidia.com/mig-<profile>`) qua cơ chế Extended Resources tiêu chuẩn.
- **Lifecycle Automation** — Idle timeout, max lifespan, và auto-purge. Khi pause (`replicas: 0`), GPU được trả lại cluster nhưng dữ liệu workspace được bảo toàn.
- **Pod Security Hardening** — Non-root (UID 1000), drop all capabilities, `automountServiceAccountToken: false`, ephemeral `/tmp` qua emptyDir.
- **Zero-Touch Infrastructure** — Ansible playbook tự động cài đặt K3s + NVIDIA Toolkit + HAMi Device Plugin trên bare-metal.

---

## Kiến Trúc

```
┌─────────────────────────────────────────────────────────────┐
│                    NotebookLab CR (YAML)                    │
│  spec: image, resources, gpu, storage, lifecycle            │
└──────────────────────────┬──────────────────────────────────┘
                           │ Reconcile
              ┌────────────▼─────────────┐
              │   Notebook Operator      │
              │   (controller-manager)   │
              └────────────┬─────────────┘
                           │ Creates & Manages
         ┌─────────┬───────┼───────┬──────────┐
         ▼         ▼       ▼       ▼          ▼
      Secret     PVC   Deployment Service   Ingress
    (JWT Token) (workspace) │     (8888)  (*.local)
                           │
                    ┌──────▼──────┐
                    │ Device Plugin│
                    │  Resources  │
                    └──────┬──────┘
                           │
                ┌──────────▼──────────┐
                │  HAMi / MIG Plugin  │
                │  nvidia.com/gpu     │
                │  nvidia.com/gpumem  │
                │  nvidia.com/mig-*   │
                └─────────────────────┘
```

### Tài Nguyên Được Quản Lý

| Resource | Mục đích |
|----------|----------|
| `Secret` | Token xác thực JupyterLab |
| `PVC` | Workspace bền vững (giữ khi pause) |
| `Deployment` | Pod JupyterLab (non-root, hardened) |
| `Service` | Expose port 8888 |
| `Ingress` | Domain + WebSocket proxy |

### GPU Providers

| Provider | Extended Resources | Yêu cầu phần cứng |
|----------|-------------------|-------------------|
| `hami` (mặc định) | `nvidia.com/gpu` + `nvidia.com/gpumem` + `nvidia.com/gpucores` | Mọi NVIDIA GPU |
| `mig` | `nvidia.com/mig-<profile>` (vd: `nvidia.com/mig-1g.5gb`) | A100, A30, H100+ |

Cả hai đều hoạt động qua cơ chế Device Plugin, không cần DRA.

---

## Yêu Cầu Hệ Thống

### Phần Cứng

| Thành phần | Tối thiểu | Khuyến nghị |
|------------|-----------|-------------|
| CPU | 4 cores | 8+ cores |
| RAM | 8 GB | 16+ GB |
| Disk | 50 GB SSD | 100+ GB SSD |
| GPU | NVIDIA với driver ≥ 535 | NVIDIA Ampere+ (A100, RTX 3090) |
| OS | Ubuntu 22.04 / 24.04 LTS | Ubuntu 24.04 LTS |

### Phần Mềm

| Dependency | Version | Ghi chú |
|------------|---------|---------|
| Kubernetes (K3s) | ≥ 1.34 | Không cần DRA feature gate |
| NVIDIA Driver | ≥ 535 | Cài sẵn trên host |
| NVIDIA Container Toolkit | latest | Ansible tự cài |
| Helm | ≥ 3.x | Cài HAMi Device Plugin |

---

## Hướng Dẫn Cài Đặt

### Bước 1 — Chuẩn Bị Cụm K3s + GPU

Chạy Ansible playbook để tự động cài đặt K3s v1.34, NVIDIA Container Toolkit, CDI, và HAMi Device Plugin:

**Cách A: Dùng playbook single-file từ GitHub Releases** (không cần clone repo)
```bash
# Tải file phát hành
curl -LO https://github.com/ngtukien/notebook-operator/releases/latest/download/cluster-setup.yml

# Chỉnh sửa inventory trong file nếu cần, sau đó chạy:
ansible-playbook cluster-setup.yml -i "localhost," -c local -K
```

**Cách B: Clone repo rồi chạy**
```bash
git clone https://github.com/ngtukien/notebook-operator.git
cd notebook-operator/ansible

# Chỉnh inventory nếu cần
vim inventory/hosts.ini

# Cài đặt cụm
ansible-playbook cluster.yml -K
```

Playbook thực hiện 5 phase tự động:

| Phase | Role | Mô tả |
|-------|------|--------|
| 1 | `nvidia` | Cài NVIDIA Container Toolkit |
| 2 | `k3s-master` | Cài K3s v1.34 (standard config, không cần DRA feature gates) |
| 3 | `k3s-worker` | Join worker nodes (bỏ qua nếu single-node) |
| 4 | `hami-node` | Sinh CDI spec cho GPU |
| 5 | `hami-master` | Cài HAMi Device Plugin + RuntimeClass |

### Bước 2 — Cài Đặt Operator

**Cách A: One-command install từ GitHub Releases**
```bash
kubectl apply -f https://github.com/ngtukien/notebook-operator/releases/latest/download/install.yaml
```

**Cách B: Cho nhà phát triển**
```bash
# Build & push image
export IMG="ghcr.io/ngtukien/notebook-operator:latest"
make docker-build docker-push IMG=$IMG

# Deploy lên cluster
make install   # Cài CRDs
make deploy IMG=$IMG
```

### Bước 3 — Tạo Notebook

```yaml
apiVersion: lab.ngtukien.id.vn/v1alpha1
kind: NotebookLab
metadata:
  name: my-notebook
  namespace: default
spec:
  image: ghcr.io/ngtukien/notebook-operator/base-image:latest
  resources:
    requests:
      cpu: "2"
      memory: 4Gi
    limits:
      cpu: "4"
      memory: 8Gi
  gpu:
    enable: true
    type: hami
    hami:
      cores: 50
      memory: 3Gi
  storage:
    workspace:
      size: 10Gi
      storageClassName: local-path
      mountPath: /workspace
    tmp:
      sizeLimit: 2Gi
  lifecycle:
    idleTimeoutMinutes: 60
    maxLifespanHours: 8
    purgeAfterInactiveDays: 7
  nodeSelector:
    gpu: "on"
```

```bash
kubectl apply -f config/samples/lab_v1alpha1_notebooklab.yaml

# Kiểm tra trạng thái
kubectl get notebooklab
```

### Trạng Thái Vòng Đời

| Phase | Ý nghĩa |
|-------|---------|
| `Provisioning` | Đang tạo PVC, Secret, Deployment |
| `Running` | Pod ready, GPU attached, có AccessURL |
| `Pausing` | Đang thu hồi Pod |
| `Paused` | GPU trả lại cluster, workspace PVC giữ nguyên |
| `Failed` | Lỗi (hết tài nguyên, sai config) |

---

## License

Apache License 2.0 — Xem [LICENSE](LICENSE).

*Phát triển bởi Nguyễn Tự Kiên (2026).*
