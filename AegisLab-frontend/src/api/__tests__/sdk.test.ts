import { describe, expect, it } from 'vitest';

import { sdkConfig } from '../sdk';

describe('SDK configuration', () => {
  it('exports a Configuration instance', () => {
    expect(sdkConfig).toBeDefined();
  });

  it('has empty basePath', () => {
    // basePath is empty because the apiClient already has /api/v2 as baseURL
    expect(sdkConfig.basePath).toBe('');
  });
});
