import { describe, expect, test } from 'vitest';
import {
  ALL_INCOMING_EDGE_TYPE,
  allIncomingEdgesToTransitions,
  createAllIncomingEdge,
  edgesToTransitions,
  isAllIncomingEdge,
  transitionsToEdges,
} from './dataTransformers.js';

describe('transitionsToEdges', () => {
  test('renders regular transitions as edges', () => {
    const edges = transitionsToEdges([
      { id: 1, workflow_id: 5, from_status_id: 2, to_status_id: 3, display_order: 0 },
    ]);
    expect(edges).toHaveLength(1);
    expect(edges[0].source).toBe('status-2');
    expect(edges[0].target).toBe('status-3');
    expect(edges[0].type).toBe('reconnectable');
  });

  test('skips initial and from-all rows', () => {
    const edges = transitionsToEdges([
      { id: 1, workflow_id: 5, from_status_id: null, to_status_id: 2, display_order: 0 },
      {
        id: 2,
        workflow_id: 5,
        from_status_id: null,
        to_status_id: 3,
        from_all_statuses: true,
        display_order: 1,
      },
      { id: 3, workflow_id: 5, from_status_id: 2, to_status_id: 3, display_order: 2 },
    ]);
    expect(edges).toHaveLength(1);
    expect(edges[0].id).toBe('edge-2-3');
  });
});

describe('all-incoming edges', () => {
  test('createAllIncomingEdge builds a special self-loop edge', () => {
    const edge = createAllIncomingEdge(7, 5);
    expect(edge.id).toBe('edge-all-7');
    expect(edge.type).toBe(ALL_INCOMING_EDGE_TYPE);
    expect(edge.source).toBe('status-7');
    expect(edge.target).toBe('status-7');
    expect(edge.data.from_all_statuses).toBe(true);
    expect(edge.data.from_status_id).toBeNull();
    expect(edge.data.to_status_id).toBe(7);
    expect(isAllIncomingEdge(edge)).toBe(true);
  });

  test('allIncomingEdgesToTransitions emits from_all_statuses rows', () => {
    const transitions = allIncomingEdgesToTransitions(
      [createEdgeRegular(), createAllIncomingEdge(7, 5), createAllIncomingEdge(9, 5)],
      5
    );
    expect(transitions).toHaveLength(2);
    expect(transitions[0]).toMatchObject({
      workflow_id: 5,
      from_status_id: null,
      from_all_statuses: true,
      to_status_id: 7,
    });
    expect(transitions[1].to_status_id).toBe(9);
  });

  test('edgesToTransitions keeps regular edges and drops all-incoming edges', () => {
    const transitions = edgesToTransitions([createEdgeRegular(), createAllIncomingEdge(7, 5)], 5);
    expect(transitions).toHaveLength(1);
    expect(transitions[0]).toMatchObject({
      workflow_id: 5,
      from_status_id: 2,
      to_status_id: 3,
    });
    expect(transitions[0].from_all_statuses).toBeUndefined();
  });
});

function createEdgeRegular() {
  return {
    id: 'edge-2-3',
    type: 'reconnectable',
    source: 'status-2',
    target: 'status-3',
    sourceHandle: 'right',
    targetHandle: 'target-left',
    data: {
      transitionId: 11,
      workflow_id: 5,
      from_status_id: 2,
      to_status_id: 3,
      display_order: 0,
    },
  };
}
