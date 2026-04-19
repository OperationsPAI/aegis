import { useMemo, useState } from 'react';

import {
  ClockCircleOutlined,
  CloudServerOutlined,
  DatabaseOutlined,
  DisconnectOutlined,
  PauseCircleOutlined,
  QuestionCircleOutlined,
  StopOutlined,
  ThunderboltOutlined,
} from '@ant-design/icons';
import { type ChaosNode, GetInjectionMetadataSystem } from '@rcabench/client';
import { useQuery } from '@tanstack/react-query';
import { Card, Empty, List, Spin, Tag, Tooltip } from 'antd';

import { injectionApi } from '../../../api/injections';
import type { FaultTypeConfig } from '../../../types/api';

import './FaultTypePanel.css';

interface FaultTypePanelProps {
  onFaultSelect: (fault: FaultTypeConfig) => void;
}

const faultTypeIcons: Record<string, React.ReactNode> = {
  cpu: <ThunderboltOutlined className='fault-type-icon' />,
  memory: <DatabaseOutlined className='fault-type-icon' />,
  disk: <CloudServerOutlined className='fault-type-icon' />,
  network: <DisconnectOutlined className='fault-type-icon' />,
  process: <StopOutlined className='fault-type-icon' />,
  io: <PauseCircleOutlined className='fault-type-icon' />,
  time: <ClockCircleOutlined className='fault-type-icon' />,
  default: <QuestionCircleOutlined className='fault-type-icon' />,
};

const faultTypeColors: Record<string, string> = {
  cpu: 'red',
  memory: 'orange',
  disk: 'blue',
  network: 'green',
  process: 'purple',
  io: 'cyan',
  time: 'gold',
  default: 'default',
};

export const FaultTypePanel: React.FC<FaultTypePanelProps> = ({
  onFaultSelect,
}: FaultTypePanelProps) => {
  const [selectedFault, setSelectedFault] = useState<FaultTypeConfig | null>(
    null
  );

  // Fetch fault metadata using the SDK
  // Use 'ts' as default system - fault_type_map is global across systems
  const {
    data: metadata,
    isLoading,
    error,
  } = useQuery({
    queryKey: ['faultMetadata'],
    queryFn: () =>
      injectionApi.getMetadata({ system: GetInjectionMetadataSystem.ts }),
  });

  // Convert fault_type_map to FaultTypeConfig array with parameters from config
  const faultTypes: FaultTypeConfig[] = useMemo(() => {
    if (!metadata?.fault_type_map) return [];

    // Define category mappings based on fault type names
    const categoryMap: Record<string, string> = {
      PodKill: 'Kubernetes',
      PodFailure: 'Kubernetes',
      ContainerKill: 'Kubernetes',
      MemoryStress: 'Memory',
      CPUStress: 'CPU',
      HTTPRequestAbort: 'Network',
      HTTPResponseAbort: 'Network',
      HTTPRequestDelay: 'Network',
      HTTPResponseDelay: 'Network',
      HTTPResponseReplaceBody: 'Network',
      HTTPResponsePatchBody: 'Network',
      HTTPRequestReplacePath: 'Network',
      HTTPRequestReplaceMethod: 'Network',
      HTTPResponseReplaceCode: 'Network',
      DNSError: 'DNS',
      DNSRandom: 'DNS',
      TimeSkew: 'Time',
      NetworkDelay: 'Network',
      NetworkLoss: 'Network',
      NetworkDuplicate: 'Network',
      NetworkCorrupt: 'Network',
      NetworkBandwidth: 'Network',
      NetworkPartition: 'Network',
      JVMLatency: 'JVM',
      JVMReturn: 'JVM',
      JVMException: 'JVM',
      JVMGarbageCollector: 'JVM',
      JVMCPUStress: 'JVM',
      JVMMemoryStress: 'JVM',
      JVMMySQLLatency: 'JVM',
      JVMMySQLException: 'JVM',
    };

    // Extract parameters from config tree
    const configChildren = metadata.config?.children || {};

    // Helper to determine parameter type from ChaosNode
    const getParameterType = (
      node: ChaosNode
    ): 'string' | 'number' | 'boolean' | 'select' | 'range' => {
      if (node.range && node.range.length === 2) {
        return 'range';
      }
      if (typeof node.value === 'number') {
        return 'number';
      }
      if (node.children) {
        // If it has children, it might be a select with options
        const childKeys = Object.keys(node.children);
        if (childKeys.length > 0) {
          return 'select';
        }
      }
      return 'string';
    };

    return Object.entries(
      metadata.fault_type_map as Record<string, string>
    ).map(([key, description], index) => {
      const faultConfig = configChildren[key];
      const parameters = faultConfig?.children
        ? (
            Object.entries(faultConfig.children) as Array<[string, ChaosNode]>
          ).map(([paramName, paramNode]) => {
            const paramType = getParameterType(paramNode);
            return {
              name: paramName,
              type: paramType,
              label: paramNode.description || paramName,
              description: paramNode.description,
              required: false,
              default: paramNode.value,
              min: paramNode.range?.[0],
              max: paramNode.range?.[1],
              options:
                paramType === 'select' && paramNode.children
                  ? Object.keys(paramNode.children)
                  : undefined,
            };
          })
        : [];

      return {
        id: index,
        name: key,
        type: key,
        category: categoryMap[key] || 'Other',
        description: description || key,
        parameters,
      };
    });
  }, [metadata]);

  const handleFaultClick = (fault: FaultTypeConfig) => {
    setSelectedFault(fault);
    onFaultSelect(fault);
  };

  const handleDragStart = (e: React.DragEvent, fault: FaultTypeConfig) => {
    e.dataTransfer.setData('application/reactflow', JSON.stringify(fault));
    e.dataTransfer.effectAllowed = 'move';
  };

  const getFaultIcon = (faultType: FaultTypeConfig) => {
    const key = faultType.category?.toLowerCase() || 'default';
    return faultTypeIcons[key] || faultTypeIcons.default;
  };

  const getFaultColor = (faultType: FaultTypeConfig) => {
    const key = faultType.category?.toLowerCase() || 'default';
    return faultTypeColors[key] || faultTypeColors.default;
  };

  const groupFaultTypeConfigsByCategory = (faultTypes: FaultTypeConfig[]) => {
    return faultTypes.reduce(
      (acc, fault) => {
        const category = fault.category || 'Other';
        if (!acc[category]) {
          acc[category] = [];
        }
        acc[category].push(fault);
        return acc;
      },
      {} as Record<string, FaultTypeConfig[]>
    );
  };

  const groupedFaultTypeConfigs = groupFaultTypeConfigsByCategory(faultTypes);

  if (error) {
    return (
      <Card title='Fault Types' className='fault-type-panel'>
        <Empty description='Failed to load fault types' />
      </Card>
    );
  }

  return (
    <Card
      title='Fault Types'
      className='fault-type-panel'
      bodyStyle={{ padding: 0 }}
    >
      {isLoading ? (
        <div style={{ padding: '24px', textAlign: 'center' }}>
          <Spin />
        </div>
      ) : (
        <div className='fault-type-list'>
          {Object.entries(groupedFaultTypeConfigs).map(([category, faults]) => (
            <div key={category} className='fault-category'>
              <div className='category-header'>
                <Tag color='blue' className='category-tag'>
                  {category}
                </Tag>
              </div>
              <List
                dataSource={faults}
                renderItem={(fault) => (
                  <List.Item
                    className={`fault-type-item ${selectedFault?.id === fault.id ? 'selected' : ''}`}
                    onClick={() => handleFaultClick(fault)}
                    draggable
                    onDragStart={(e) => handleDragStart(e, fault)}
                    style={{ cursor: 'grab' }}
                  >
                    <div className='fault-type-content'>
                      <div className='fault-type-header'>
                        {getFaultIcon(fault)}
                        <span className='fault-type-name'>{fault.name}</span>
                        <Tag
                          color={getFaultColor(fault)}
                          className='fault-type-tag'
                        >
                          {fault.type}
                        </Tag>
                      </div>
                      <div className='fault-type-description'>
                        {fault.description}
                      </div>
                      {fault.parameters && (
                        <div className='fault-type-params'>
                          <Tooltip
                            title={`${fault.parameters.length} parameters`}
                          >
                            <Tag>{fault.parameters.length} params</Tag>
                          </Tooltip>
                        </div>
                      )}
                    </div>
                  </List.Item>
                )}
              />
            </div>
          ))}
          {faultTypes.length === 0 && (
            <Empty description='No fault types available' />
          )}
        </div>
      )}
    </Card>
  );
};
