import { createApp } from 'vue'
import './styles/app.css'
import App from './App.vue'
import { router } from './router'
import { bootstrapAuth } from './stores/auth'
import { loadSiteConfig } from './stores/site'

// 启动即请求 GET /auth/me（未登录时回退到游客 CSRF token），路由守卫会等待该结果
void bootstrapAuth()
// 公开注册开关需要尽早加载，导航与注册页依赖它
void loadSiteConfig()

createApp(App).use(router).mount('#app')
