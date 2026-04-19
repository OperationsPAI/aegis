/**
 * Type declarations for @finos/perspective packages (v3.x)
 * Handles WASM URL imports and module augmentation
 */

// Vite ?url imports for WASM files
declare module '@finos/perspective/dist/wasm/perspective-server.wasm?url' {
  const url: string;
  export default url;
}

declare module '@finos/perspective-viewer/dist/wasm/perspective-viewer.wasm?url' {
  const url: string;
  export default url;
}

// Side-effect plugin imports
declare module '@finos/perspective-viewer-d3fc' {
  // Side-effect import: registers D3FC chart plugins
}

declare module '@finos/perspective-viewer-datagrid' {
  // Side-effect import: registers datagrid plugin
}

// CSS module declarations
declare module '@finos/perspective-viewer/dist/css/pro.css' {
  const css: string;
  export default css;
}

declare module '@finos/perspective-viewer/dist/css/pro-dark.css' {
  const css: string;
  export default css;
}

declare module '@finos/perspective-viewer/dist/css/themes.css' {
  const css: string;
  export default css;
}

// Augment JSX to support <perspective-viewer> custom element
declare global {
  namespace JSX {
    interface IntrinsicElements {
      'perspective-viewer': React.DetailedHTMLProps<
        React.HTMLAttributes<HTMLElement> & {
          ref?: React.Ref<HTMLElement>;
          theme?: string;
        },
        HTMLElement
      >;
    }
  }
}

export {};
