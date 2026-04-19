export interface FaultEntry {
  action: string;
  mode: string;
  duration: string;
  params: Record<string, string>;
}

export interface AlgorithmSelection {
  containerId: number;
  name: string;
  version: string;
}
