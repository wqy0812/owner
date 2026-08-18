import { describe, expect, it } from 'vitest';
import {
  environmentConstraintGroups,
  formatConstraintValue,
  parseConstraintSelection,
  serializeConstraintSelection,
  toggleConstraintValue,
} from '../types/environmentConstraints';

describe('environment constraint display', () => {
  it('orders and labels architecture, OS and IP family', () => {
    expect(environmentConstraintGroups({
      ipFamily: ['IPv4', 'IPv6'],
      operatingSystem: ['SUSE', 'Kylin'],
      architecture: ['amd64', 'arm64'],
    })).toEqual([
      { key: 'architecture', label: '架构', values: ['x86/amd64', 'ARM/arm64'] },
      { key: 'operatingSystem', label: '操作系统', values: ['SUSE', 'Kylin'] },
      { key: 'ipFamily', label: 'IP 协议族', values: ['IPv4', 'IPv6'] },
    ]);
  });

  it('accepts seed aliases such as cpuArch and osDistro', () => {
    expect(environmentConstraintGroups({
      cpuArch: 'x86_64',
      osDistro: ['kylin'],
      hardwareProfile: ['gpu'],
    })).toEqual([
      { key: 'cpuArch', label: '架构', values: ['x86/amd64'] },
      { key: 'osDistro', label: '操作系统', values: ['Kylin'] },
      { key: 'hardwareProfile', label: '硬件类型', values: ['GPU'] },
    ]);
  });

  it('returns an empty list when the release has no constraints', () => {
    expect(environmentConstraintGroups(undefined)).toEqual([]);
    expect(environmentConstraintGroups({})).toEqual([]);
    expect(formatConstraintValue('amd64')).toBe('x86/amd64');
  });

  it('normalizes aliases into a selectable contract and serializes canonical keys', () => {
    const selection = parseConstraintSelection({
      cpuArch: ['x86_64', 'aarch64'],
      osDistro: ['Kylin V10'],
      network: 'ipv4',
    });
    expect(selection.architecture).toEqual(['amd64', 'arm64']);
    expect(selection.operatingSystem).toEqual(['Kylin']);
    expect(selection.ipFamily).toEqual(['IPv4']);
    expect(serializeConstraintSelection(toggleConstraintValue(selection, 'architecture', 'amd64'))).toEqual({
      architecture: ['arm64'],
      operatingSystem: ['Kylin'],
      ipFamily: ['IPv4'],
    });
  });
});
