import { useState } from 'react';
import { useNavigate } from 'react-router-dom';

import type { ContainerResp } from '@rcabench/client';
import { message } from 'antd';

import { projectApi } from '@/api/projects';

import type { AlgorithmSelection, FaultEntry } from '../types';

interface SubmitParams {
  projectId: number;
  projectName: string;
  pedestalContainer: ContainerResp;
  pedestalVersion: string;
  benchmarkContainer: ContainerResp;
  benchmarkVersion: string;
  faultSpecs: FaultEntry[];
  injectionInterval: number;
  preDuration: number;
  selectedAlgorithms: AlgorithmSelection[];
}

export function useInjectionSubmit() {
  const navigate = useNavigate();
  const [submitting, setSubmitting] = useState(false);

  const handleSubmit = async (params: SubmitParams) => {
    const {
      projectId,
      projectName,
      pedestalContainer,
      pedestalVersion,
      benchmarkContainer,
      benchmarkVersion,
      faultSpecs,
      injectionInterval,
      preDuration,
      selectedAlgorithms,
    } = params;

    setSubmitting(true);
    try {
      // Build specs as ChaosNode[][] — single fault group with all entries
      const specs = [
        faultSpecs.map((f) => ({
          action: f.action,
          mode: f.mode,
          duration: f.duration,
          params: f.params,
        })),
      ];

      const reqData = {
        project_name: projectName,
        pedestal: {
          name: pedestalContainer.name,
          version: pedestalVersion,
        },
        benchmark: {
          name: benchmarkContainer.name,
          version: benchmarkVersion,
        },
        interval: injectionInterval,
        pre_duration: preDuration,
        specs,
        ...(selectedAlgorithms.length > 0 && {
          algorithms: selectedAlgorithms.map((a) => ({
            name: a.name,
            version: a.version,
          })),
        }),
      };

      const result = await projectApi.submitInjection(projectId, reqData);

      const traceId =
        result && typeof result === 'object' && 'trace_id' in result
          ? (result as { trace_id?: string }).trace_id
          : undefined;

      message.success(
        traceId
          ? `Injection submitted successfully (trace: ${traceId})`
          : 'Injection submitted successfully'
      );

      navigate(`/projects/${projectId}/datapacks`);
    } catch (err: unknown) {
      const msg =
        err instanceof Error ? err.message : 'Failed to submit injection';
      message.error(msg);
    } finally {
      setSubmitting(false);
    }
  };

  return { submitting, handleSubmit };
}
