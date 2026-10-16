import { createApp } from 'vue'
import './styles/tokens.css'
import './styles/ui.css'
import App from './App.vue'
import { router } from './router'
import { bootstrapAuth } from './stores/auth'

// 启动即请求 GET /auth/me（未登录时回退到游客 CSRF token），路由守卫会等待该结果
void bootstrapAuth()

createApp(App).use(router).mount('#app')
