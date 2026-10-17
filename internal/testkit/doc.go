// Package testkit 是目标架构下的测试脚手架：构造隔离测试 schema 的数据库资源、
// 注入式测试服务与带 Cookie/CSRF 的 HTTP 客户端，供集成测试与 HTTP 单元测试复用。
//
// 设计目标：
//   - 不依赖旧 internal/server 的构造签名；服务由组合根（internal/app）装配，
//     测试只通过本包暴露的基座操作资源；
//   - 集成测试默认跳过，只有 TIMELINE_INTEGRATION=1 时才连接开发实例中的
//     timeline_test / user_test 隔离 schema；
//   - 共享测试 schema 不做物理删除，账号等数据按“缺失则创建、存在则修复”幂等准备；
//   - 一次性 schema（首注册引导等需要空库的用例）由 IsolatedEnvOptions 创建，
//     cleanup 时只 DROP 本测试自建的 schema。
package testkit
