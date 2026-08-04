# KubeEdu - Jupiter Operator Infrastructure (Ansible)

Thư mục này chứa toàn bộ kịch bản Ansible (Playbooks & Roles) để tự động hóa việc xây dựng một cụm Kubernetes (K3s) hỗ trợ GPU mạnh mẽ. Cụm này được thiết kế đặc biệt để chạy **Jupiter Operator** với các công nghệ cắt GPU ảo hóa như **HAMi** và chuẩn **DRA (Dynamic Resource Allocation)**.

## Kiến trúc 5 Phase (Roles)

Quá trình cài đặt được chia nhỏ thành 5 giai đoạn (Phases) chạy tuần tự và hoàn toàn tự động:

1. **Phase 1 (NVIDIA Toolkit):** Tự động thêm repo và cài đặt các thư viện `nvidia-container-toolkit` để Kernel hệ điều hành nhận diện được GPU.
2. **Phase 2 (K3s Master):** Triển khai K3s Server lên Master Node. Khởi tạo API Server.
3. **Phase 3 (K3s Worker):** Kết nạp (Join) các Worker Nodes vào cụm Master tự động thông qua Token.
4. **Phase 4 (HAMi Node - CDI):** Sinh ra các file CDI (Container Device Interface) tại `/etc/cdi/` để K3s/Containerd có thể giao tiếp với phần cứng GPU của NVIDIA.
5. **Phase 5 (HAMi Master):** Triển khai **HAMi-DRA** qua Helm Chart, đồng thời kích hoạt Cert-Manager để xử lý các Webhook chặn và dịch yêu cầu (Mutating Webhook) từ Device Plugin sang chuẩn DRA hiện đại.

---

## 🚀 Hướng Dẫn Sử Dụng

### Yêu cầu tiên quyết
- Các Node phải là Ubuntu 22.04 LTS.
- Đã cắm phần cứng NVIDIA GPU và đã cài Driver NVIDIA gốc trên host.
- Đã cài đặt Ansible trên máy tính chạy lệnh.

### Bước 1: Cấu hình Inventory
Mở file `inventory/hosts.ini` và điền IP/User của các Server thực tế:
```ini
[master]
192.168.1.100 ansible_user=ngtukien

[worker]
# 192.168.1.101 ansible_user=ngtukien

[k3s_cluster:children]
master
worker
```
*(Nếu cài đặt thử nghiệm trên máy local, cấu hình `localhost` như mặc định).*

### Bước 2: Bắt đầu Triển khai (Deploy)
Do việc can thiệp hạ tầng cần đặc quyền `root`, bạn bắt buộc phải truyền cờ `-K` để Ansible hỏi mật khẩu `sudo` của máy tính:

```bash
ansible-playbook cluster.yml -K
```
*Ghi chú: Khi terminal hiện chữ `BECOME password:`, hãy nhập mật khẩu máy tính của bạn.*

---

## 🧹 Hướng Dẫn Gỡ Bỏ (Uninstall / Reset Cụm)

Trong quá trình phát triển (Dev/Test), nếu bạn làm hỏng cụm và muốn **xóa sạch mọi thứ làm lại từ đầu**, hãy chạy các lệnh sau trực tiếp trên Terminal của Node đó:

### Gỡ K3s (Xóa cụm K8s)
K3s có sẵn 2 script dọn dẹp hệ thống cực kỳ sạch sẽ:

- **Trên Worker Nodes:** Gõ lệnh sau để thoát khỏi cụm:
  ```bash
  /usr/local/bin/k3s-agent-uninstall.sh
  ```
- **Trên Master Node:** Gõ lệnh sau để phá hủy toàn bộ API Server và Database:
  ```bash
  /usr/local/bin/k3s-uninstall.sh
  ```

### Gỡ bỏ thư mục cấu hình CDI của GPU
```bash
sudo rm -rf /etc/cdi
```

### Xóa toàn bộ file Kubeconfig trên máy local
```bash
rm -rf ~/.kube/config
rm -f ../k3s.yaml
```

Sau khi chạy các lệnh trên, máy chủ của bạn đã sạch tinh tươm. Bạn có thể chạy lại `ansible-playbook cluster.yml -K` để cài lại một cụm mới trong 2 phút!
