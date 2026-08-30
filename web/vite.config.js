import { defineConfig } from 'vite'
import vue from '@vitejs/plugin-vue'
import { resolve } from 'path'

export default defineConfig({
  plugins: [vue()],
  resolve: {
    alias: {
      '@': resolve(__dirname, 'src')
    }
  },
  // UI-09: 补齐构建配置。
  //
  // 原配置完全没有 build 段：
  //  1. echarts 是全局全量引入（import * as echarts），与 element-plus、vue
  //     一起被打进单个 1MB+ 的 chunk，内网离线场景首屏加载很慢，且没有体积告警兜底；
  //  2. 未设置 base，构建产物默认使用绝对路径 /assets/...，
  //     部署在非根路径时会 404。
  build: {
    // 使用相对路径，支持任意子目录部署
    base: './',
    // 体积告警阈值。
    // element-plus 在本项目里仍以完整包引入（离线单二进制工具，不依赖 CDN，
    // 且组件使用面很广，逐组件按需引入需额外引入 unplugin-vue-components），
    // 因此阈值按"element 整包 + 业务代码"的实际量级设定。
    chunkSizeWarningLimit: 1000,
    rollupOptions: {
      output: {
        // 按依赖来源手动分包：业务代码与第三方库分离后，
        // 业务迭代不会让 vendor 的缓存整体失效。
        manualChunks: {
          echarts: ['echarts'],
          vue: ['vue', 'vue-router', 'pinia'],
          element: ['element-plus', '@element-plus/icons-vue'],
          vendor: ['axios']
        }
      }
    }
  },
  server: {
    port: 5173,
    proxy: {
      '/api': {
        target: 'http://localhost:8080',
        changeOrigin: true
      }
    }
  }
})
