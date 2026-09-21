# Windows Computer Use 预览版升级

这是 GrothKeiran/agentdock fork 的预览版，不是上游官方发布。

## 是否需要卸载旧版？

正常情况下不需要。官方 v0.8.3 支持保留安装目录、配置和凭据，直接运行新版 `AgentDockSetup-amd64.exe` 覆盖升级。Windows ARM64 应使用对应的 ARM64 安装包。

`.1` / `.2` 预览版存在安装器兼容问题：安装器尚未提交新版本时调用稳定启动器，启动器拒绝安装器的试运行版本；回滚后又尝试让官方旧版执行其不支持的 `service task-start`。这会留下 `failed / external_rollback_failed` 状态，阻止下一次安装。管理员增强模式的计划任务也会在试运行期间经过稳定启动器。

`.3` 修复安装辅助命令的来源，并让启动器在安装事务匹配且安装器仍持有进程锁时启动试运行版本。托盘界面改为安装提交后启动；中断的安装仍不会被当作成功安装放行。

## 已经安装失败怎么办？

请保留原目录，不要删除 `install/transaction.json`，也不要盲目执行 `install abandon`。

对于**从官方 v0.8.3 普通模式升级到 `.1` / `.2` 失败**的已知状态：

1. 从 [fork Releases](https://github.com/GrothKeiran/agentdock/releases) 下载 `.3` 的 Windows ZIP，完整解压到新文件夹，不要覆盖原安装目录。
2. 使用原先登录的 Windows 用户双击解压目录中的 `Repair-AgentDock.cmd`，不要切换到另一个管理员账户。
3. 看到 `Original v0.8.3 recovered` 后，再运行 `.3` 对应架构的 Setup。

恢复工具默认检查 `%LOCALAPPDATA%\AgentDock`。自定义安装目录可由 PowerShell 调用 `repair-windows-preview.ps1 -InstallRoot '你的安装目录'`。

工具会验证失败事务、原版本、安装目录、账户凭据和启动项，备份事务信息，恢复旧 Core 并检查健康状态，最后才确认回滚完成。遇到其他版本、管理员模式的失败事务、目录不匹配或凭据无法读取时会拒绝自动恢复，请保留错误信息进一步诊断。

Windows 安装包未签名，可能显示 SmartScreen 提示。预览版的 Computer Use 应只授予可信 AI 客户端，并先在不涉及敏感操作的场景验证；安装测试通过不等于所有桌面控制场景均已验证。
