# CMP 50HX 20 ГБ

[Главная](../README_RU.md) · [Версия 10 ГБ](10GB_RU.md)

## Проверенное оборудование

Проверка выполнена 26–27 августа 2026 года на четырёх картах:

```text
PCI device: 10de:1e09
subsystem:  10de:1554
VRAM:       20480 MiB на каждой карте
driver:     NVIDIA Open 610.43.03
kernel:     6.8.0-137-generic и 6.8.0-138-generic
BDF:        01:00.0, 03:00.0, 81:00.0, 82:00.0
```

## Почему старый патч не запускал 20 ГБ

Старый путь использовал WPR-границы, соответствующие проверенной 10-ГБ карте.
На 20-ГБ плате штатный FWSEC создаёт другой диапазон:

```text
WPR high:low = 04ffee00:04ffe000
span         = 00000e00
```

Из-за несовпадения NVIDIA Booter завершался кодом `0x8d`, затем GSP-RM
достигал лимита попыток и `nvidia-smi` не видел устройства.

Обновлённый `01-cmp50-stockflow.patch` получает WPR после штатного FWSEC
отдельно для каждого PCI BDF, принимает только span `0xe00`, сохраняет
`low/high` в per-BDF state и останавливается при изменении геометрии. Это не
подстановка адреса одной карты, а динамический fail-closed путь для 10/20 ГБ.

## Фактические результаты

| Этап | Результат на 4 × 20 ГБ |
| --- | --- |
| Dynamic WPR / GSP | Все карты прошли `REFWSEC_STOCK_WPR_PASS`, `INPLACE_STOCK_BOOTER_AFTER_UNLOCK_PASS`, `CMP50_GSP_READY` |
| VRAM | Все четыре карты видны, по `20480 MiB` |
| Compute | `PASS_CMP50HX_ISSUE_RATE_AND_COUNTS` |
| CUDA / Tensor | 3584 / 448 |
| RT count | RM сообщает 56; физические RT-инструкции не работают |
| ReBAR после загрузки | На всех четырёх картах PCI Region 1 = 16 ГиБ и `nvidia-smi BAR1 Total = 16384 MiB` |
| Ядро 6.8.0-138 | RT+ReBAR: все 4 карты GSP ready, 20480 МиБ, compute verifier PASS |
| Gen2 capability | Endpoint успешно объявляет 5 GT/s |
| Физический Gen2 | 5 GT/s x16 на трёх картах и 5 GT/s x4 на слоте x4 после одного Link Disable/Enable |
| Автоматический Gen2 retrain | Не готов: обычный Retrain bit возвращает `RETRAIN_FAIL`; Gen2-сборка на ядре 138 приводит к Booter `0x8d` |
| Idle governor | На этой 20-ГБ системе ещё не проверен |

## Безопасная установка

По умолчанию выбирается стадия `rt` — максимально проверенная на 20 ГБ:

```bash
git clone https://github.com/rjohny55/cmp50-unlock.git
cd cmp50-unlock
sudo ./install.sh --stage rt --install-userland --initramfs
sudo systemctl poweroff
```

После холодного старта:

```bash
sudo ./verify-live.sh
```

ReBAR устанавливается только следующим отдельным этапом:

```bash
sudo ./install.sh --stage rebar --initramfs
sudo systemctl poweroff
```

После старта одновременно проверьте `lspci -vv` Region 1 и
`nvidia-smi -q -d MEMORY` → `BAR1 Memory Usage / Total`. Только Region 1 = 16G
недостаточно: драйвер также должен сообщить большой BAR1.

## Gen2 — экспериментальный этап

`04-cmp50-pcie-gen2.patch` повторно открывает XVE PCIe shadow после GSP. Это
доказанно меняет endpoint `LnkCap` с 2.5 на 5 GT/s. Однако стандартный PCIe
Retrain не переводит физическую линию на тестовом X99 host. Полный Link
Disable/Enable переводит, но разрывает активный RM; безопасный двухпроходный
boot helper ещё разрабатывается.

На ядре `6.8.0-138` отдельно проверены стадии `rt`, `rebar` и `gen2`: первые
две запускают все карты, а модуль с добавленным `04` воспроизводимо завершает
GSP Booter кодом `0x8d`. Поэтому `install.sh --stage gen2` теперь требует
дополнительный флаг `--allow-experimental-gen2`. Это не означает, что режим
безопасен: установщик не выполняет Link Disable и не включает Gen2.

`tools/cmp50-bar0-link-rate.c` — fail-closed исследовательская утилита для
внутренней команды link-rate. Она проверяет точные PCI/subsystem ID и считает
чтение `0xffffffff` ошибкой. Утилита ещё не является boot helper и не должна
запускаться автоматически.

## Idle governor

Скрипты находятся в `idle-governor/`, но для 20 ГБ не включаются по умолчанию.
Обязательно проверяйте возврат полных частот под нагрузкой.
