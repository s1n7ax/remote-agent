import { describe, expect, it } from 'vitest';
import { proofArtifactId } from './proof';

describe('proofArtifactId', () => {
  it('reads the id of an artifact link', () => {
    expect(proofArtifactId('/api/artifacts/0a1b2c')).toBe('0a1b2c');
  });
  it('treats every other reference as unresolved', () => {
    for (const src of ['.ship/g/verify/3/1-after.png', '1-after.png', '/api/artifacts/', '/api/artifacts/a/b', 'https://x/api/artifacts/a', 'data:image/png;base64,AA', '', null, undefined]) {
      expect(proofArtifactId(src)).toBeNull();
    }
  });
});
