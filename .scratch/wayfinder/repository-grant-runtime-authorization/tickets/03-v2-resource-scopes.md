---
title: V2 单仓库 scope
label: wayfinder:task
state: closed
---

## Question

哪些现有 V2 Repository 路由能够安全地下放到 read/write/admin scope，且不扩大资源创建或
全局管理权限？

## Resolution

单仓库详情、retention 读取、Maven session/artifact 读取使用 read；disable、publish 和
artifact 删除使用 write；grants 与 retention 替换使用 admin。全局列表、创建与 Hosted
Group 生命周期仍为管理员 bootstrap 操作。
