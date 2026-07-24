---
title: 原生协议与 Conan 绑定
label: wayfinder:task
state: closed
---

## Question

如何使运行时 grant 规则覆盖 Native Maven、OCI、Raw 与 Conan read-through，且不改变
各协议的认证挑战和响应约定？

## Resolution

原生 hosted 路径在实际 Repository 上执行授权并保持原协议拒绝响应。Conan member 以
稳定 `repositoryId` 显式绑定 `format: conan` Repository；未绑定 member 保留遗留策略，
运行时不从名称或 endpoint 推断绑定关系。
