import type React from 'react';

import {
  CheckCircleOutlined,
  CloseCircleOutlined,
  LoadingOutlined,
  MinusCircleOutlined,
} from '@ant-design/icons';
import { Steps } from 'antd';

/**
 * Datapack state progression stepper.
 *
 * Maps numeric datapack state (0-6) onto three pipeline steps:
 *   Step 0 – Inject  (states 0, 1, 2)
 *   Step 1 – Build   (states 3, 4)
 *   Step 2 – Detect  (states 5, 6)
 */

interface StateStepperProps {
  /** Current datapack state (0-6). Accepts number or numeric string. */
  state: number | string;
}

type StepStatus = 'wait' | 'process' | 'finish' | 'error';

interface StepDef {
  title: string;
  status: StepStatus;
  icon: React.ReactNode;
}

const iconForStatus = (status: StepStatus): React.ReactNode => {
  switch (status) {
    case 'finish':
      return <CheckCircleOutlined style={{ color: '#52c41a' }} />;
    case 'error':
      return <CloseCircleOutlined style={{ color: '#ff4d4f' }} />;
    case 'process':
      return <LoadingOutlined style={{ color: '#1677ff' }} />;
    case 'wait':
    default:
      return <MinusCircleOutlined style={{ color: '#d9d9d9' }} />;
  }
};

const computeSteps = (state: number): StepDef[] => {
  let inject: StepStatus = 'wait';
  let build: StepStatus = 'wait';
  let detect: StepStatus = 'wait';

  switch (state) {
    case 0: // Initial
      inject = 'process';
      break;
    case 1: // InjectFailed
      inject = 'error';
      break;
    case 2: // InjectSuccess
      inject = 'finish';
      build = 'process';
      break;
    case 3: // BuildFailed
      inject = 'finish';
      build = 'error';
      break;
    case 4: // BuildSuccess
      inject = 'finish';
      build = 'finish';
      detect = 'process';
      break;
    case 5: // DetectorFailed
      inject = 'finish';
      build = 'finish';
      detect = 'error';
      break;
    case 6: // DetectorSuccess
      inject = 'finish';
      build = 'finish';
      detect = 'finish';
      break;
    default:
      break;
  }

  return [
    { title: 'Inject', status: inject, icon: iconForStatus(inject) },
    { title: 'Build', status: build, icon: iconForStatus(build) },
    { title: 'Detect', status: detect, icon: iconForStatus(detect) },
  ];
};

const StateStepper: React.FC<StateStepperProps> = ({ state }) => {
  const numericState = typeof state === 'string' ? Number(state) : state;
  const steps = computeSteps(numericState);

  // current = index of the first non-finish step (or last step if all finish)
  const current = steps.findIndex((s) => s.status !== 'finish');

  return (
    <Steps
      size='small'
      current={current === -1 ? steps.length - 1 : current}
      items={steps.map((s) => ({
        title: s.title,
        status: s.status,
        icon: s.icon,
      }))}
    />
  );
};

export default StateStepper;
