import vue from '@vitejs/plugin-vue'
import tailwindcss from '@tailwindcss/vite'
import { defineConfig } from 'vite'

// 开发环境把后端路径代理到本地 Go 服务（容器内端口固定 5070，见 cmd/server/main.go）
const backend = 'http://localhost:5070'

export default defineConfig({
  plugins: [vue(), tailwindcss()],
  server: {
    proxy: {
      '/api': backend,
      '/health': backend,
      '/healthz': backend,
      '/readyz': backend,
    },
  },
})
