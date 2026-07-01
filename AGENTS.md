# AGENTS.md

## 发布规则

- 开发完成后只提醒当前版本号，并询问用户是否需要推送对应版本标签，例如：`当前版本为 0.2.18，是否需要推送 v0.2.18 版本标签？`
- 未得到用户明确确认前，禁止自动执行 `git tag`、`git push origin <tag>`、`gh release create` 或任何会触发 Release 发布的操作。
- 用户明确要求发布后，才可以推送版本标签并检查 GitHub Actions / Release 资产。
