import { describe, expect, it } from 'vitest';

import { WorkspaceInviteManager } from './workspace-invite';

type Ref = {
  publisher: string;
  boot_time: number;
  seq_num: number;
};

const compare = (a: Ref, b: Ref): number => (
  (WorkspaceInviteManager as unknown as {
    compareMlsRefPubs(left: Ref, right: Ref): number;
  }).compareMlsRefPubs(a, b)
);

describe('MLS reference ordering', () => {
  it('orders sequence numbers within one publisher session', () => {
    const older = { publisher: '/alice', boot_time: 10, seq_num: 2 };
    const newer = { publisher: '/alice', boot_time: 10, seq_num: 3 };
    expect(compare(older, newer)).toBeLessThan(0);
  });

  it('does not compare sequence numbers across publishers', () => {
    const alice = { publisher: '/alice', boot_time: 10, seq_num: 99 };
    const bob = { publisher: '/bob', boot_time: 10, seq_num: 1 };
    expect(compare(alice, bob)).toBeLessThan(0);
  });

  it('orders boot sessions before publisher and sequence number', () => {
    const earlier = { publisher: '/z', boot_time: 9, seq_num: 99 };
    const later = { publisher: '/a', boot_time: 10, seq_num: 1 };
    expect(compare(earlier, later)).toBeLessThan(0);
  });
});
