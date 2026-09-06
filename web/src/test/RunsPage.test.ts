import { expect, it } from 'vitest';
import { scenarioRunModeLabel, scenarioStepLabel } from '../pages/RunsPage';

 it('distinguishes upgrade, baseline verification and business acceptance in Run detail', () => {
   expect(scenarioRunModeLabel('upgrade')).toBe('场景升级');
   expect(scenarioRunModeLabel('upgrade', 'scenario_test')).toBe('场景升级测试');
   expect(scenarioRunModeLabel('baseline_verify')).toBe('恢复后基线复核');
   expect(scenarioStepLabel({ id: 'accept', name: 'business check', status: 'succeeded', sourceType: 'scenario_acceptance', phase: 'acceptance' })).toBe('场景业务验收');
   expect(scenarioStepLabel({ id: 'verify', name: 'target check', status: 'succeeded', stage: 'target_verify', phase: 'postcheck' })).toContain('目标集群验证');
 });
