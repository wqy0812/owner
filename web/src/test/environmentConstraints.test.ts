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

  it('does not reinterpret unknown fields or values as first-version constraints', () => {
    expect(environmentConstraintGroups({
      cpuArch: 'x86_64',
      osDistro: ['kylin'],
      hardwareProfile: ['gpu'],
    })).toEqual([
      { key: 'hardwareProfile', label: '硬件类型', values: ['GPU'] },
      { key: 'cpuArch', label: 'cpuArch', values: ['x86_64'] },
      { key: 'osDistro', label: 'osDistro', values: ['kylin'] },
    ]);
  });

  it('returns an empty list when the release has no constraints', () => {
    expect(environmentConstraintGroups(undefined)).toEqual([]);
    expect(environmentConstraintGroups({})).toEqual([]);
    expect(formatConstraintValue('amd64')).toBe('x86/amd64');
  });

  it('parses and serializes only canonical first-version fields and values', () => {
    const selection = parseConstraintSelection({
      architecture: ['amd64', 'arm64'],
      operatingSystem: ['Kylin'],
      ipFamily: 'IPv4',
      cpuArch: ['x86_64'],
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
