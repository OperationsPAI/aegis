import React, { type DragEvent, useCallback, useRef } from 'react';
import ReactFlow, {
  addEdge,
  Background,
  type Connection,
  ConnectionMode,
  Controls,
  type Edge,
  MiniMap,
  type Node,
  Panel,
  ReactFlowProvider,
  useEdgesState,
  useNodesState,
  useReactFlow,
} from 'reactflow';
import 'reactflow/dist/style.css';

import {
  ClearOutlined,
  PlayCircleOutlined,
  PlusOutlined,
} from '@ant-design/icons';
import { Button, message, Space } from 'antd';

import type { FaultTypeConfig } from '../../../types/api';

import { FaultNode } from './FaultNode';

import './VisualCanvas.css';

const nodeTypes = {
  fault: FaultNode,
};

interface VisualCanvasProps {
  faultMatrix: FaultTypeConfig[][];
  onFaultMatrixChange: (matrix: FaultTypeConfig[][]) => void;
  selectedFault: FaultTypeConfig | null;
}

let id = 0;
const getId = () => `fault-node-${id++}`;

const VisualCanvasContent: React.FC<VisualCanvasProps> = ({
  faultMatrix,
  onFaultMatrixChange,
}) => {
  const reactFlowWrapper = useRef<HTMLDivElement>(null);
  const { project } = useReactFlow();
  const [nodes, setNodes, onNodesChange] = useNodesState([]);
  const [edges, setEdges, onEdgesChange] = useEdgesState([]);

  const handleConfigureFault = useCallback((fault: FaultTypeConfig) => {
    message.info(`Configuring ${fault.name} fault...`);
  }, []);

  const handleDeleteNode = useCallback(
    (nodeId: string) => {
      setNodes((nds) => nds.filter((node) => node.id !== nodeId));
      setEdges((eds) =>
        eds.filter((edge) => edge.source !== nodeId && edge.target !== nodeId)
      );

      // Update fault matrix
      const newMatrix = faultMatrix
        .map((batch) =>
          batch.filter((_, index) => !nodeId.includes(`-${index}-`))
        )
        .filter((batch) => batch.length > 0);

      onFaultMatrixChange(newMatrix);
    },
    [faultMatrix, onFaultMatrixChange, setNodes, setEdges]
  );

  // Convert fault matrix to nodes and edges
  const updateNodesFromMatrix = useCallback(() => {
    const newNodes: Node[] = [];
    const newEdges: Edge[] = [];

    faultMatrix.forEach((batch, batchIndex) => {
      const batchY = batchIndex * 200;

      // Add batch header node
      newNodes.push({
        id: `batch-${batchIndex}`,
        type: 'input',
        position: { x: 50, y: batchY },
        data: { label: `Batch ${batchIndex + 1}` },
        style: {
          background: 'var(--color-primary-light)',
          border: '2px solid var(--color-primary-500)',
          borderRadius: '8px',
          padding: '8px 16px',
          fontWeight: '600',
        },
      });

      // Add fault nodes in this batch
      batch.forEach((fault, faultIndex) => {
        const nodeId = getId();
        newNodes.push({
          id: nodeId,
          type: 'fault',
          position: { x: 200 + faultIndex * 180, y: batchY },
          data: {
            fault,
            onDelete: (nodeId: string) => handleDeleteNode(nodeId),
            onConfigure: (fault: FaultTypeConfig) =>
              handleConfigureFault(fault),
          },
        });

        // Connect to batch header
        newEdges.push({
          id: `edge-batch-${batchIndex}-fault-${faultIndex}`,
          source: `batch-${batchIndex}`,
          target: nodeId,
          animated: true,
          style: { stroke: 'var(--color-primary-500)' },
        });
      });
    });

    setNodes(newNodes);
    setEdges(newEdges);
  }, [faultMatrix, setNodes, setEdges, handleDeleteNode, handleConfigureFault]);

  // Update nodes when matrix changes
  React.useEffect(() => {
    updateNodesFromMatrix();
  }, [updateNodesFromMatrix]);

  const onConnect = useCallback(
    (params: Edge | Connection) => setEdges((eds) => addEdge(params, eds)),
    [setEdges]
  );

  const onDragOver = useCallback((event: DragEvent) => {
    event.preventDefault();
    event.dataTransfer.dropEffect = 'move';
  }, []);

  const onDrop = useCallback(
    (event: DragEvent) => {
      event.preventDefault();

      const faultTypeJson = event.dataTransfer.getData('application/reactflow');
      if (!faultTypeJson) return;

      try {
        const fault: FaultTypeConfig = JSON.parse(faultTypeJson);

        if (!reactFlowWrapper.current) return;

        const reactFlowBounds =
          reactFlowWrapper.current.getBoundingClientRect();
        const position = project({
          x: event.clientX - reactFlowBounds.left,
          y: event.clientY - reactFlowBounds.top,
        });

        // Determine which batch to add to based on Y position
        const batchIndex = Math.floor(position.y / 200);
        const newMatrix = [...faultMatrix];

        // Ensure batch exists
        if (!newMatrix[batchIndex]) {
          newMatrix[batchIndex] = [];
        }

        // Add fault to batch
        newMatrix[batchIndex].push(fault);

        onFaultMatrixChange(newMatrix);
        message.success(`Added ${fault.name} to batch ${batchIndex + 1}`);
      } catch {
        message.error('Failed to add fault');
      }
    },
    [project, faultMatrix, onFaultMatrixChange]
  );

  const handleAddBatch = useCallback(() => {
    const newMatrix = [...faultMatrix, []];
    onFaultMatrixChange(newMatrix);
    message.success(`Added batch ${newMatrix.length}`);
  }, [faultMatrix, onFaultMatrixChange]);

  const handleClearCanvas = useCallback(() => {
    setNodes([]);
    setEdges([]);
    onFaultMatrixChange([]);
    message.success('Canvas cleared');
  }, [onFaultMatrixChange, setNodes, setEdges]);

  const handleAutoArrange = useCallback(() => {
    // Auto-arrange nodes in a grid pattern
    const newNodes = nodes.map((node, index) => {
      if (node.type === 'fault') {
        const batchIndex = Math.floor(index / 3);
        const faultIndex = index % 3;
        return {
          ...node,
          position: {
            x: 200 + faultIndex * 180,
            y: batchIndex * 200,
          },
        };
      }
      return node;
    });
    setNodes(newNodes);
    message.success('Nodes auto-arranged');
  }, [nodes, setNodes]);

  return (
    <div className='visual-canvas' ref={reactFlowWrapper}>
      <ReactFlow
        nodes={nodes}
        edges={edges}
        onNodesChange={onNodesChange}
        onEdgesChange={onEdgesChange}
        onConnect={onConnect}
        onInit={(reactFlowInstance) => {
          reactFlowInstance.fitView();
        }}
        onDrop={onDrop}
        onDragOver={onDragOver}
        nodeTypes={nodeTypes}
        connectionMode={ConnectionMode.Loose}
        fitView
        attributionPosition='bottom-left'
      >
        <Controls position='top-left' />
        <MiniMap
          position='bottom-right'
          style={{ backgroundColor: 'var(--color-secondary-100)' }}
          nodeColor={(node) => {
            if (node.type === 'fault') return 'var(--color-primary-500)';
            return 'var(--color-secondary-500)';
          }}
          nodeStrokeWidth={3}
        />
        <Background color='var(--color-secondary-400)' gap={16} />

        <Panel position='top-right' className='canvas-toolbar'>
          <Space>
            <Button
              type='primary'
              icon={<PlusOutlined />}
              size='small'
              onClick={handleAddBatch}
            >
              Add Batch
            </Button>
            <Button
              icon={<PlayCircleOutlined />}
              size='small'
              onClick={handleAutoArrange}
            >
              Auto Arrange
            </Button>
            <Button
              danger
              icon={<ClearOutlined />}
              size='small'
              onClick={handleClearCanvas}
            >
              Clear
            </Button>
          </Space>
        </Panel>

        {nodes.length === 0 && (
          <div className='empty-canvas'>
            <div className='empty-canvas-icon'>
              <PlayCircleOutlined />
            </div>
            <div className='empty-canvas-text'>
              <p>Drag fault types from the left panel</p>
              <p>or click &quot;Add Batch&quot; to start</p>
            </div>
          </div>
        )}
      </ReactFlow>
    </div>
  );
};

export const VisualCanvas: React.FC<VisualCanvasProps> = (props) => {
  return (
    <ReactFlowProvider>
      <VisualCanvasContent {...props} />
    </ReactFlowProvider>
  );
};
