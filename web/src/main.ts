import { createApp } from 'vue'
import App from './App.vue'
import {preventImplicitSave} from './explicitSave'
import './styles.css'
document.addEventListener('keydown',preventImplicitSave,true)
createApp(App).mount('#app')
