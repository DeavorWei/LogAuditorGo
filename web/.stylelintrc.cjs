module.exports = {
  extends: [
    'stylelint-config-standard',
    'stylelint-config-recommended-vue'
  ],
  rules: {
    // 令牌铁律：禁止在业务属性中书写 Hex 色值（强制消费 tokens.css）
    'declaration-property-value-disallowed-list': {
      '/(color|background|border|fill|stroke)/': ['/#[0-9a-fA-F]{3,8}/'], // 禁止硬编码十六进制色
      '/^(margin|padding)(-top|-right|-bottom|-left)?$/': ['/-\\d+px/'],   // 禁止负间距 hack
      'border-radius': ['/^(?!var\\(--radius|0|50%)/'],                   // 圆角必须走令牌（豁免 0 与圆形 50%）
    },
    'length-zero-no-unit': true,
    'selector-max-id': 0,
    'declaration-no-important': null,                                      // !important 在 Phase 4 专项治理
    'selector-class-pattern': null,                                        // 允许 Element Plus 等命名规范
    'color-function-notation': null,
    'alpha-value-notation': null,
    'color-hex-length': null,
    // Vue 3 穿透与全局伪类适配（:deep / :global 属于伪类选择器，非 at-rule）
    'selector-pseudo-class-no-unknown': [true, { ignorePseudoClasses: ['deep', 'global'] }],
    'no-duplicate-selectors': true,
  },
  // 必须豁免令牌定义、主题层、Reset 降级与图表目录
  ignoreFiles: [
    'src/styles/tokens.css',
    'src/styles/element-theme.css',
    'src/styles/base.css',     // 含有 prefers-reduced-motion 的 !important 降级
    'src/styles/dialog.css',   // 含有全屏弹窗尺寸覆盖
    '**/echarts/**'
  ],
}
