import {
  DatapackState,
  DatapackStateString,
  ExecutionState,
  ExecutionStateString,
} from '@rcabench/client';

/**
 * Generic mapper to link Numeric Enums to their String Enum counterparts.
 *
 * T: The Numeric Enum type
 * K: The String Enum type
 */
function getEnumNameByValue<T extends object, K extends object>(
  numericEnum: T,
  stringEnum: K,
  value: T[keyof T]
): K[keyof K] {
  const key = (numericEnum as Record<string | number, string>)[value as number];
  const nameKey = `${key}Name`;
  return (stringEnum as Record<string, K[keyof K]>)[nameKey];
}

/**
 * Specific wrapper for ExecutionState
 */
export const getExecutionStateName = (value: ExecutionState) =>
  getEnumNameByValue(ExecutionState, ExecutionStateString, value);

/**
 * Specific wrapper for DatapackState
 */
export const getDatapackStateName = (value: DatapackState) =>
  getEnumNameByValue(DatapackState, DatapackStateString, value);
