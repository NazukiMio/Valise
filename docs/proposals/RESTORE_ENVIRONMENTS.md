# DEPRECATED：并行模式、直通模式与完整重建测试

> **DEPRECATED（2026-11-15）**：本提案已于 2026-11-15 由用户决定正式撤销并归档。Valise 不再实施 A/B 双槽位并行运行环境、`linux-bwrap` 容器接管或三档恢复模式切换，统一收敛回暂存校验 + 带交换日志的原子目录交换 + `.valise-prev-<op>` 保留与一键回退。
>
> 历史原文已只读归档至：[docs/history/RESTORE_ENVIRONMENTS_DEPRECATED_2026-11-15.md](../history/RESTORE_ENVIRONMENTS_DEPRECATED_2026-11-15.md)
> 现行恢复规范见：[docs/ARCHITECTURE.md](../ARCHITECTURE.md) 第 8 节 与 [docs/PRODUCT.md](../PRODUCT.md) 第 6.3 节。
