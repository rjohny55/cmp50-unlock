# Инструкция агенту: сборка, установка и откат CMP50 Unlock

Документ предназначен для автоматизированного кодинг-агента на Linux-сервере с NVIDIA CMP 50HX. Цель: проверить совместимость, собрать изменённые `open-gpu-kernel-modules` версии `610.43.03`, подготовить безопасную установку, получить подтверждение пользователя, установить модули и проверить результат после перезагрузки.

Актуальные варианты платы описаны отдельно: [10 ГБ](docs/10GB_RU.md) и
[20 ГБ](docs/20GB_RU.md). Для 20 ГБ стадии `stockflow`, `rt` и `rebar`
проверены на эталонной системе с ядрами 6.8.0-137/138, но ReBAR всё равно
нужно подтвердить на каждом новом host. `gen2` не является рабочей стадией:
на 6.8.0-138 она воспроизводимо вызывает Booter `0x8d`.

## 1. Обязательные правила

1. Работать только на host-системе, не внутри обычного Docker-контейнера.
2. Не отключать Secure Boot, не выгружать активный NVIDIA driver, не менять initramfs и не перезагружать сервер без отдельного подтверждения пользователя.
3. До установки создать проверяемый backup текущих NVIDIA modules и записать команды отката.
4. Не продолжать при несовпадении PCI device/subsystem ID.
5. Не удалять проверки целевой платы из патчей и не переносить регистры на другую модель GPU.
6. Не выполнять установку на production-сервере без окна обслуживания и аварийного доступа.
7. После каждой фазы показывать результат и следующую потенциально опасную операцию.
8. При неоднозначности остановиться, ничего не устанавливать и сообщить точный blocker.

## 2. Поддерживаемая конфигурация

```text
GPU PCI ID:       10de:1e09
Subsystem ID:     10de:1554 или 1462:371f
Архитектура:      TU102 / CMP 50HX
Driver source:    NVIDIA open-gpu-kernel-modules 610.43.03
Source SHA-256:   9df87d753cd9c05aa0eedc462af9b35debb549a657136e863282f94c96ee2640
```

Если обнаружен другой GPU, subsystem, driver branch или неподходящее ядро — остановиться.

## 3. Фаза A — диагностика без изменений

```bash
set -euo pipefail
uname -a
cat /etc/os-release
lspci -Dnn | grep -iE 'VGA|3D|NVIDIA'
nvidia-smi || true
modinfo nvidia | sed -n '1,30p' || true
find "/lib/modules/$(uname -r)" -type f \
  \( -name 'nvidia.ko' -o -name 'nvidia.ko.*' \
     -o -name 'nvidia-uvm.ko' -o -name 'nvidia-uvm.ko.*' \) -print
command -v mokutil >/dev/null && mokutil --sb-state || true
df -h / /var /tmp
free -h
test -d "/lib/modules/$(uname -r)/build"
```

Показать пользователю точное ядро, дистрибутив, PCI/subsystem ID, версию и путь текущих modules, состояние Secure Boot, headers и свободное место. Завершить фазу заключением `совместимо` или `остановлено`.

## 4. Фаза B — исходники и патчи

```bash
set -euo pipefail
repo_root="$(pwd)"
kernel_release="$(uname -r)"
work_root="$(mktemp -d /var/tmp/cmp50-unlock-build.XXXXXX)"
source_archive="${work_root}/open-gpu-kernel-modules-610.43.03.tar.gz"
source_dir="${work_root}/source"

curl -L --fail --show-error -o "${source_archive}" \
  https://github.com/NVIDIA/open-gpu-kernel-modules/archive/refs/tags/610.43.03.tar.gz
printf '%s  %s\n' \
  9df87d753cd9c05aa0eedc462af9b35debb549a657136e863282f94c96ee2640 \
  "${source_archive}" | sha256sum -c -

mkdir -p "${work_root}/extract"
tar -xzf "${source_archive}" -C "${work_root}/extract"
extracted_dir="$(find "${work_root}/extract" -mindepth 1 -maxdepth 1 -type d | head -n 1)"
test -n "${extracted_dir}"
mv "${extracted_dir}" "${source_dir}"
```

Применять патчи по стадиям, сначала выполняя dry-run. Без явного выбора
используется проверенная стадия `rt`:

```bash
CMP50_PATCH_STAGE=stockflow bash ./build.sh
CMP50_PATCH_STAGE=rt bash ./build.sh
CMP50_PATCH_STAGE=rebar bash ./build.sh
CMP50_PATCH_STAGE=gen2 bash ./build.sh  # только исследование, не установка
```

Ручной эквивалент полной стадии применяет четыре patch-файла строго по порядку:

```bash
for patch_name in \
  01-cmp50-stockflow.patch \
  02-cmp50-rt-core-count.patch \
  03-cmp50-rebar.patch \
  04-cmp50-pcie-gen2.patch
do
  test -s "${repo_root}/${patch_name}"
  patch --dry-run -d "${source_dir}" -p1 < "${repo_root}/${patch_name}"
  patch -d "${source_dir}" -p1 < "${repo_root}/${patch_name}"
done
```

При ошибке запрещено использовать `--force`, fuzz или пропускать hunks. Остановиться и показать конфликт.

## 5. Фаза C — сборка без установки

```bash
make -C "${source_dir}" modules \
  -j"$(nproc)" KERNEL_UNAME="${kernel_release}"

artifact_dir="${work_root}/artifacts"
mkdir -p "${artifact_dir}"
for module_name in nvidia nvidia-uvm nvidia-modeset nvidia-drm nvidia-peermem
do
  module_path="${source_dir}/kernel-open/${module_name}.ko"
  test ! -f "${module_path}" || \
    install -m 0644 "${module_path}" "${artifact_dir}/${module_name}.ko"
done

test -s "${artifact_dir}/nvidia.ko"
(cd "${artifact_dir}" && sha256sum ./*.ko > checksums.sha256)
test "$(modinfo -F version "${artifact_dir}/nvidia.ko")" = "610.43.03"
test "$(modinfo -F vermagic "${artifact_dir}/nvidia.ko" | awk '{print $1}')" = "${kernel_release}"
grep -a -q 'CMP50_STOCKFLOW_' "${artifact_dir}/nvidia.ko"
grep -a -q 'CMP50_GSP_READY_' "${artifact_dir}/nvidia.ko"
grep -a -q 'CMP50_REBAR' "${artifact_dir}/nvidia.ko"
(cd "${artifact_dir}" && sha256sum -c checksums.sha256)
```

Показать путь к артефактам, `modinfo`, контрольные суммы и результаты проверок. Подтвердить, что активный driver не изменён. Затем остановиться и запросить разрешение на установку.

## 6. Фаза D — backup

Только после подтверждения пользователя:

```bash
backup_root="/var/lib/cmp50-unlock/backups/$(date -u +%Y%m%dT%H%M%SZ)"
install -d -m 0700 "${backup_root}/modules"

for module_name in nvidia nvidia_uvm nvidia_modeset nvidia_drm nvidia_peermem
do
  current_path="$(modinfo -n "${module_name}" 2>/dev/null || true)"
  if test -n "${current_path}" && test -f "${current_path}"; then
    cp -a --parents "${current_path}" "${backup_root}/modules/"
  fi
done

cp -a "/boot/initrd.img-${kernel_release}" "${backup_root}/" 2>/dev/null || true
cp -a /etc/modprobe.d "${backup_root}/modprobe.d" 2>/dev/null || true
uname -a > "${backup_root}/uname.txt"
nvidia-smi -q > "${backup_root}/nvidia-smi-before.txt" 2>&1 || true
lspci -Dnnvv > "${backup_root}/lspci-before.txt"
find "${backup_root}" -type f -exec sha256sum {} \; \
  > "${backup_root}/backup-checksums.sha256"
```

Убедиться, что backup содержит текущий `nvidia.ko`. Показать точный путь backup и процедуру отката.

## 7. Secure Boot

Если Secure Boot включён, агент обязан использовать уже согласованный и зарегистрированный MOK key. Нельзя автоматически отключать Secure Boot или регистрировать новый ключ.

```bash
sign_file="/lib/modules/${kernel_release}/build/scripts/sign-file"
test -x "${sign_file}"
test -f "${MOK_PRIVATE_KEY:?}"
test -f "${MOK_CERTIFICATE:?}"
for module_path in "${artifact_dir}"/*.ko; do
  "${sign_file}" sha256 "${MOK_PRIVATE_KEY}" "${MOK_CERTIFICATE}" "${module_path}"
done
```

Если доверенного ключа нет — остановиться до решения пользователя.

## 8. Фаза E — установка для следующей загрузки

Не выгружать NVIDIA modules на работающем сервере.

```bash
install_dir="/lib/modules/${kernel_release}/updates/cmp50-unlock"
install -d -m 0755 "${install_dir}"
install -m 0644 "${artifact_dir}"/*.ko "${install_dir}/"
depmod -a "${kernel_release}"
modinfo -n nvidia
modinfo -F version nvidia

if command -v update-initramfs >/dev/null 2>&1; then
  update-initramfs -u -k "${kernel_release}"
elif command -v dracut >/dev/null 2>&1; then
  dracut --force "/boot/initramfs-${kernel_release}.img" "${kernel_release}"
else
  printf '%s\n' 'Не найден генератор initramfs' >&2
  exit 1
fi
```

Показать установочный каталог, выбранный путь `modinfo`, результат initramfs, команду перезагрузки и команду отката. Получить отдельное подтверждение перезагрузки. Не выполнять `rmmod`, GPU reset или reboot без него.

## 9. Фаза F — проверка после перезагрузки

```bash
uname -r
lsmod | grep '^nvidia'
modinfo -n nvidia
modinfo -F version nvidia
nvidia-smi
lspci -Dnnvv | grep -A35 -i 'NVIDIA'
journalctl -k -b --no-pager | grep -E \
  'CMP50_|NVRM|Xid|AER|PCIe|BAR1|REBAR' || true
```

Проверить загрузку `610.43.03`, доступность всех GPU, отсутствие новых Xid/AER/oops, ожидаемые CMP50 markers, BAR1, PCIe link и короткую CUDA-нагрузку. При критической ошибке перейти к откату.

## 10. Откат

Предпочтительно загрузиться в предыдущее рабочее ядро или recovery mode:

```bash
kernel_release="$(uname -r)"
rm -rf "/lib/modules/${kernel_release}/updates/cmp50-unlock"
depmod -a "${kernel_release}"

if command -v update-initramfs >/dev/null 2>&1; then
  update-initramfs -u -k "${kernel_release}"
elif command -v dracut >/dev/null 2>&1; then
  dracut --force "/boot/initramfs-${kernel_release}.img" "${kernel_release}"
fi
```

Если исходные modules были перезаписаны, восстановить их из `backup_root`, снова выполнить `depmod` и обновить initramfs. Перезагрузку для отката тоже выполнять только после подтверждения пользователя.

## 11. Итоговый отчёт агента

```text
Host/kernel:
GPU PCI IDs и subsystem IDs:
Secure Boot:
Предыдущая NVIDIA version/path:
Новая module version/path:
Source SHA-256:
Patch dry-run:
Build result и checksums:
Backup path:
Installation/initramfs result:
Reboot approved/performed:
Post-reboot nvidia-smi:
CMP50 markers:
Xid/AER/kernel errors:
BAR1/PCIe state:
Rollback path/command:
Final status: success / rolled back / blocked
```

## 12. Особенность импортированного snapshot

Импортированный `build.sh` ссылается на `patches/` и `verify/rm_issue_rate.c`, но опубликованный snapshot содержит patch-файлы в корне и не содержит `verify/rm_issue_rate.c`. Поэтому нельзя слепо считать `bash ./build.sh` готовой установкой. Эта инструкция использует отдельный воспроизводимый путь сборки из корневых patch-файлов и стандартные проверки modules.

Перед исправлением `build.sh` следует восстановить verifier из подтверждённого первоисточника либо убрать зависимость отдельным коммитом и добавить эквивалентные тесты.
