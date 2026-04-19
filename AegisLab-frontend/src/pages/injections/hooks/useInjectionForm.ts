import { useCallback, useMemo, useState } from 'react';

import type { ContainerResp } from '@rcabench/client';
import { message } from 'antd';

import type { AlgorithmSelection, FaultEntry } from '../types';

export function useInjectionForm() {
  // Step navigation
  const [currentStep, setCurrentStep] = useState(0);

  // Step 1 - Pedestal
  const [pedestalContainer, setPedestalContainer] =
    useState<ContainerResp | null>(null);
  const [pedestalVersion, setPedestalVersion] = useState('');

  // Step 2 - Benchmark
  const [benchmarkContainer, setBenchmarkContainer] =
    useState<ContainerResp | null>(null);
  const [benchmarkVersion, setBenchmarkVersion] = useState('');

  // Step 3 - Faults
  const [faultSpecs, setFaultSpecs] = useState<FaultEntry[]>([
    { action: '', mode: '', duration: '30s', params: {} },
  ]);

  // Step 4 - Timing
  const [injectionInterval, setInjectionInterval] = useState(5);
  const [preDuration, setPreDuration] = useState(3);

  // Step 5 - Algorithms
  const [selectedAlgorithms, setSelectedAlgorithms] = useState<
    AlgorithmSelection[]
  >([]);

  // ---- Validation per step ----
  const canProceed = useMemo(() => {
    switch (currentStep) {
      case 0:
        return !!pedestalContainer && !!pedestalVersion;
      case 1:
        return !!benchmarkContainer && !!benchmarkVersion;
      case 2:
        return (
          faultSpecs.length > 0 &&
          faultSpecs.every((f) => f.action && f.mode && f.duration)
        );
      case 3:
        return injectionInterval > 0 && preDuration >= 0;
      case 4:
        return true; // algorithms are optional
      default:
        return true;
    }
  }, [
    currentStep,
    pedestalContainer,
    pedestalVersion,
    benchmarkContainer,
    benchmarkVersion,
    faultSpecs,
    injectionInterval,
    preDuration,
  ]);

  const next = useCallback(() => {
    if (!canProceed) {
      message.warning('Please complete all required fields before proceeding.');
      return;
    }
    setCurrentStep((s) => Math.min(s + 1, 5));
  }, [canProceed]);

  const prev = useCallback(() => {
    setCurrentStep((s) => Math.max(s - 1, 0));
  }, []);

  // ---- Fault helpers ----
  const addFault = () => {
    setFaultSpecs((prev) => [
      ...prev,
      { action: '', mode: '', duration: '30s', params: {} },
    ]);
  };

  const removeFault = (idx: number) => {
    setFaultSpecs((prev) => prev.filter((_, i) => i !== idx));
  };

  const updateFault = (idx: number, patch: Partial<FaultEntry>) => {
    setFaultSpecs((prev) =>
      prev.map((f, i) => (i === idx ? { ...f, ...patch } : f))
    );
  };

  return {
    currentStep,
    canProceed,
    next,
    prev,

    pedestalContainer,
    setPedestalContainer,
    pedestalVersion,
    setPedestalVersion,

    benchmarkContainer,
    setBenchmarkContainer,
    benchmarkVersion,
    setBenchmarkVersion,

    faultSpecs,
    addFault,
    removeFault,
    updateFault,

    injectionInterval,
    setInjectionInterval,
    preDuration,
    setPreDuration,

    selectedAlgorithms,
    setSelectedAlgorithms,
  };
}
