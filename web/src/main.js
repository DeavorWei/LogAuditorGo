import { createApp } from 'vue'
import { createPinia } from 'pinia'
import ElementPlus from 'element-plus'
import 'element-plus/dist/index.css'
import zhCn from 'element-plus/es/locale/lang/zh-cn'

import App from './App.vue'
import router from './router'
import { registerGlobalIcons } from './plugins/icons'

const app = createApp(App)

/**
 * WEB-15 / UI-14: 原实现 `for (const [k, c] of Object.entries(ElementPlusIconsVue))`
 * 把上千个图标组件全部注册为全局组件，首屏 chunk 因此膨胀数百 KB。
 *
 * 本项目的模板中已经全部使用"具名导入 + 局部注册"的写法，
 * 全局注册表只需保留通过字符串名动态引用的少量图标（如 el-button 的 icon 属性）。
 * 需要新增时在此处补一行即可，不必再拉全量图标库。
 */
registerGlobalIcons(app)

app.use(createPinia())
app.use(router)
app.use(ElementPlus, { locale: zhCn })

app.mount('#app')
