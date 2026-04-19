import { Button, Card, Steps } from 'antd';

import { useProjectContext } from '@/hooks/useProjectContext';

import { useInjectionForm } from './hooks/useInjectionForm';
import { useInjectionSubmit } from './hooks/useInjectionSubmit';
import AlgorithmStep from './steps/AlgorithmStep';
import ContainerVersionStep from './steps/ContainerVersionStep';
import FaultStep from './steps/FaultStep';
import ReviewStep from './steps/ReviewStep';
import TimingStep from './steps/TimingStep';

const STEP_ITEMS = [
  { title: 'Pedestal' },
  { title: 'Benchmark' },
  { title: 'Faults' },
  { title: 'Timing' },
  { title: 'Algorithms' },
  { title: 'Review' },
];

const InjectionWizard: React.FC = () => {
  const { projectName, projectId, project, isLoading } = useProjectContext();
  const form = useInjectionForm();
  const { submitting, handleSubmit } = useInjectionSubmit();

  const onSubmit = () => {
    if (!projectId || !form.pedestalContainer || !form.benchmarkContainer)
      return;

    handleSubmit({
      projectId,
      projectName: project?.name ?? projectName ?? '',
      pedestalContainer: form.pedestalContainer,
      pedestalVersion: form.pedestalVersion,
      benchmarkContainer: form.benchmarkContainer,
      benchmarkVersion: form.benchmarkVersion,
      faultSpecs: form.faultSpecs,
      injectionInterval: form.injectionInterval,
      preDuration: form.preDuration,
      selectedAlgorithms: form.selectedAlgorithms,
    });
  };

  const renderStepContent = () => {
    switch (form.currentStep) {
      case 0:
        return (
          <ContainerVersionStep
            containerType={2}
            label='Pedestal'
            selectedContainer={form.pedestalContainer}
            selectedVersion={form.pedestalVersion}
            onContainerChange={form.setPedestalContainer}
            onVersionChange={form.setPedestalVersion}
          />
        );
      case 1:
        return (
          <ContainerVersionStep
            containerType={1}
            label='Benchmark'
            selectedContainer={form.benchmarkContainer}
            selectedVersion={form.benchmarkVersion}
            onContainerChange={form.setBenchmarkContainer}
            onVersionChange={form.setBenchmarkVersion}
          />
        );
      case 2:
        return (
          <FaultStep
            faultSpecs={form.faultSpecs}
            onAdd={form.addFault}
            onRemove={form.removeFault}
            onUpdate={form.updateFault}
          />
        );
      case 3:
        return (
          <TimingStep
            injectionInterval={form.injectionInterval}
            onIntervalChange={form.setInjectionInterval}
            preDuration={form.preDuration}
            onPreDurationChange={form.setPreDuration}
          />
        );
      case 4:
        return (
          <AlgorithmStep
            value={form.selectedAlgorithms}
            onChange={form.setSelectedAlgorithms}
          />
        );
      case 5:
        return (
          <ReviewStep
            projectName={project?.name ?? projectName ?? ''}
            pedestalContainer={form.pedestalContainer}
            pedestalVersion={form.pedestalVersion}
            benchmarkContainer={form.benchmarkContainer}
            benchmarkVersion={form.benchmarkVersion}
            injectionInterval={form.injectionInterval}
            preDuration={form.preDuration}
            faultSpecs={form.faultSpecs}
            selectedAlgorithms={form.selectedAlgorithms}
          />
        );
      default:
        return null;
    }
  };

  if (isLoading) {
    return <Card loading />;
  }

  return (
    <Card>
      <Steps
        current={form.currentStep}
        items={STEP_ITEMS}
        style={{ marginBottom: 24 }}
      />
      <div style={{ minHeight: 300 }}>{renderStepContent()}</div>
      <div
        style={{
          marginTop: 24,
          display: 'flex',
          justifyContent: 'space-between',
        }}
      >
        <div>
          {form.currentStep > 0 && (
            <Button onClick={form.prev}>Previous</Button>
          )}
        </div>
        <div>
          {form.currentStep < 5 && (
            <Button
              type='primary'
              onClick={form.next}
              disabled={!form.canProceed}
            >
              Next
            </Button>
          )}
          {form.currentStep === 5 && (
            <Button type='primary' onClick={onSubmit} loading={submitting}>
              Submit Injection
            </Button>
          )}
        </div>
      </div>
    </Card>
  );
};

export default InjectionWizard;
