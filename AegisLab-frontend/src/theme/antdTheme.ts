/**
 * Ant Design 5 ConfigProvider theme — Rosetta lab-instrument language.
 *
 * Seed tokens drive the global look (primary = pure ink, off-white surface,
 * Inter UI, 13 px base). Component tokens patch the high-frequency mismatches:
 *   - Button: pill (radius 20), not the AntD default 6.
 *   - Card:   panel radius 16.
 *   - Table:  hairline divider, no header tint.
 *
 * Values are duplicated here (not `var(...)`) because AntD theme tokens are
 * computed in JS — they cannot resolve CSS custom properties.
 */

import type { ThemeConfig } from 'antd';

const FONT_UI =
  "'Inter Variable', 'Inter', -apple-system, BlinkMacSystemFont, 'Segoe UI', " +
  "'PingFang SC', 'Hiragino Sans GB', 'Microsoft YaHei', sans-serif";

export const aegisTheme: ThemeConfig = {
  cssVar: true,
  hashed: false,
  token: {
    colorPrimary: '#000000',
    colorInfo: '#000000',
    colorSuccess: '#000000',
    colorWarning: '#e11d48',
    colorError: '#e11d48',

    colorBgLayout: '#f5f5f7',
    colorBgContainer: '#ffffff',
    colorBgElevated: '#ffffff',
    colorBgSpotlight: '#000000',

    colorText: '#000000',
    colorTextSecondary: '#555555',
    colorTextTertiary: 'rgba(0, 0, 0, 0.55)',
    colorTextQuaternary: 'rgba(0, 0, 0, 0.35)',

    colorBorder: 'rgba(0, 0, 0, 0.08)',
    colorBorderSecondary: 'rgba(0, 0, 0, 0.04)',

    borderRadius: 8,
    borderRadiusLG: 16,
    borderRadiusSM: 4,
    borderRadiusXS: 2,

    fontFamily: FONT_UI,
    fontSize: 13,
    fontSizeLG: 14,
    fontSizeSM: 11,

    lineHeight: 1.5,

    controlHeight: 32,
    controlHeightSM: 24,

    boxShadow: '0 2px 8px rgba(0, 0, 0, 0.02)',
    boxShadowSecondary: '0 2px 8px rgba(0, 0, 0, 0.02)',

    motionDurationMid: '0.2s',
    motionDurationFast: '0.15s',
    motionDurationSlow: '0.25s',
  },
  components: {
    Button: {
      borderRadius: 20,
      borderRadiusLG: 24,
      borderRadiusSM: 16,
      controlHeight: 32,
      paddingInline: 16,
      fontWeight: 500,
      defaultBg: 'transparent',
      defaultColor: '#000000',
      defaultBorderColor: '#000000',
      primaryShadow: 'none',
      defaultShadow: 'none',
      dangerShadow: 'none',
    },
    Card: {
      borderRadiusLG: 16,
      headerBg: 'transparent',
      headerFontSize: 16,
      headerHeight: 52,
      paddingLG: 20,
    },
    Table: {
      headerBg: 'transparent',
      headerSplitColor: 'transparent',
      headerColor: '#000000',
      borderColor: 'rgba(0, 0, 0, 0.08)',
      cellPaddingBlock: 8,
    },
    Input: {
      borderRadius: 8,
      activeBorderColor: '#000000',
      hoverBorderColor: '#000000',
    },
    InputNumber: {
      borderRadius: 8,
    },
    Select: {
      borderRadius: 8,
    },
    Tag: {
      borderRadiusSM: 4,
    },
    Modal: {
      borderRadiusLG: 16,
    },
    Drawer: {
      colorBgElevated: '#ffffff',
    },
    Menu: {
      itemBg: 'transparent',
      itemSelectedBg: '#000000',
      itemSelectedColor: '#ffffff',
      itemHoverBg: 'rgba(0, 0, 0, 0.04)',
      itemBorderRadius: 8,
    },
    Tabs: {
      inkBarColor: '#000000',
      itemSelectedColor: '#000000',
      itemHoverColor: '#000000',
    },
    Switch: {
      colorPrimary: '#000000',
      colorPrimaryHover: '#1a1a1a',
    },
    Tooltip: {
      colorBgSpotlight: '#000000',
      colorTextLightSolid: '#ffffff',
    },
    Progress: {
      defaultColor: '#000000',
      remainingColor: 'rgba(0, 0, 0, 0.06)',
    },
  },
};

export default aegisTheme;
