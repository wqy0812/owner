import {HostGroupName} from './HostGroupName';
import { Info, ListOrdered } from 'lucide-react';
import type { ComponentTestPlan } from '../types/domain';
import { StatusPill } from './Primitives';
export const phaseLabel = (phase?: string) => ({ pre: '前置检查', execute: '执行动作', post: '后置检查', check: '独立检查', acceptance: '场景业务验收' }[phase ?? ''] ?? phase ?? '执行动作');
export function JobPlanPreview({ plan }: { plan: ComponentTestPlan }) {
  return <section className="test-plan-preview" aria-label="完整执行计划">
    <header><div className="test-plan-preview__heading"><span className="panel__icon"><ListOrdered size={17} aria-hidden="true" /></span><div><strong>单作业执行计划</strong><small>Environment Revision {plan.environmentRevisionId}</small></div></div><StatusPill status={plan.requiresApproval ? 'awaiting_approval' : 'ready'}>{plan.requiresApproval ? '需环境 Owner 审批' : '可排队执行'}</StatusPill></header>
    <div>{plan.steps.map(step => <article key={`${step.order}-${step.actionId}`}><span>{step.order}</span><div><strong>{step.componentName || step.name || '场景'} · {step.stage === 'target_verify' ? '目标集群检查' : phaseLabel(step.phase)}</strong><p>{step.releaseVersion} · {step.action} · <HostGroupName value={step.limit}/></p>{(step.phase === 'pre' || step.phase === 'post' || step.phase === 'check') && <p>{step.name} · <code>{step.playbook}</code>{step.phase === 'post' && step.rollbackSourceActionId && <small>复用被回滚动作的前置检查</small>}</p>}{step.backupRef && <small>恢复基线：{step.backupRef}</small>}</div></article>)}</div>
    <footer><Info size={14} aria-hidden="true" /><p>按计划完成当前动作及其检查后，才进入下一组件；回滚默认执行回滚和回滚后检查。</p></footer>
  </section>;
}
