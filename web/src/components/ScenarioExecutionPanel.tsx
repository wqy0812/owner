import { ModalFooter } from './ModalFooter';
import { useModalBusy } from './ModalBusyContext';
import { Table } from 'antd';
import { Field } from './Field';
import { Select, Button } from 'antd';
import { ExecutionPreparationPanel } from './ExecutionPreparationPanel';
import { useEffect, useState } from 'react';
import { ArrowUpRight, Beaker, CheckCircle2, CircleDashed, Minus, Plus, Trash2 } from 'lucide-react';
import { api, actionableExplanation } from '../api/client';
import { displayError, useApp } from '../context/AppContext';
import { StatusExplanationPanel } from './StatusExplanationPanel';
import { InfoNote } from './Primitives';
import { newScenarioClientID } from '../features/scenarios/model';
import type { Environment, ScenarioEdge, ScenarioExecutionMode, ScenarioExecutionPreview, ScenarioRevision, WorkExplanation } from '../types/domain';
const MODE_LABELS: Record<ScenarioExecutionMode, string> = { install: '完整安装', upgrade: '升级已有集群', baseline_verify: '恢复后基线复核' };
const CHANGE_LABELS: Record<string, string> = { install: '新增', added: '新增', add: '新增', upgrade: '更换版本', changed: '更换版本', configure: '有效参数变化', uninstall: '删除', removed: '删除', remove: '删除', unchanged: '无变化', none: '无变化' };
const STAGE_LABELS: Record<string, string> = { source_verify: '来源组件验证', target_verify: '完整目标验证', change: '组件变更', acceptance: '场景业务验收', pre: '前置检查', execute: '执行动作', post: '后置检查', check: '独立检查' };
const ACTION_LABELS: Record<string, string> = { install: '安装', upgrade: '升级', configure: '配置', uninstall: '卸载', check: '检查', acceptance: '业务验收' };
export function ScenarioExecutionPanel({ revision, environments, editable, canLaunch, initialMode = 'install', blockedReason, environmentIssues, onSubmitted, onSaved }: {
    revision: ScenarioRevision;
    environments: Environment[];
    editable: boolean;
    canLaunch: boolean;
    initialMode?: ScenarioExecutionMode;
    blockedReason?: string;
    environmentIssues?: (environment: Environment) => string[];
    onSubmitted?: () => void;
    onSaved: () => void;
}) {
    const { notify, signalRefresh } = useApp();
    blockedReason = blockedReason || (!revision.nodes.length ? '当前 DAG 为空；请先添加组件节点并保存草稿。' : undefined);
    const [environmentId, setEnvironmentId] = useState('');
    const [mode, setMode] = useState<ScenarioExecutionMode>(initialMode);
    const [plan, setPlan] = useState<ScenarioExecutionPreview>();
    const [busy, setBusy] = useState(false);
    useModalBusy(Boolean(busy));
    const [explanation, setExplanation] = useState<WorkExplanation>();
    const [constraints, setConstraints] = useState<ScenarioEdge[]>(revision.upgradeConstraints ?? []);
    const [from, setFrom] = useState('');
    const [to, setTo] = useState('');
    const [constraintsDirty, setConstraintsDirty] = useState(false);
    const [attemptId, setAttemptId] = useState('');
    const testOnly = revision.state !== 'released' && mode !== 'baseline_verify';
    useEffect(() => { setPlan(undefined); setExplanation(undefined); setAttemptId(''); }, [revision.id, revision.revisionDigest, revision.state, environmentId, mode, blockedReason]);
    useEffect(() => { setConstraints(revision.upgradeConstraints ?? []); setConstraintsDirty(false); }, [revision.id, revision.revisionDigest]);
    async function execute() {
        if (!plan?.ready)
            return;
        setBusy(true);
        try {
            const input = { executionMode: mode, expectedPlanDigest: plan.planDigest, idempotencyKey: attemptId };
            if (testOnly)
                await api.testScenario(revision.id, environmentId, input);
            else
                await api.runScenario(revision.id, environmentId, input);
            notify('success', `${MODE_LABELS[mode]}${testOnly ? '测试' : ''}已提交`, '运行中心可查看审批、步骤与验收结果。');
            setPlan(undefined);
            signalRefresh(['scenarios', 'runs', 'environments', 'workbench']);
            onSubmitted?.();
        }
        catch (reason) {
            setExplanation(actionableExplanation(reason));
            notify('error', '执行提交失败', displayError(reason));
        }
        finally {
            setBusy(false);
        }
    }
    async function saveConstraints() {
        if (!revision.revisionDigest)
            return;
        setBusy(true);
        try {
            await api.saveScenarioUpgradeConstraints(revision.id, constraints, revision.revisionDigest);
            setConstraintsDirty(false);
            setPlan(undefined);
            onSaved();
            notify('success', '升级顺序已保存', '请重新预览并取得两类测试证据。');
        }
        catch (reason) {
            notify('error', '保存升级顺序失败', displayError(reason));
        }
        finally {
            setBusy(false);
        }
    }
    const allNodes = [...new Map([...revision.nodes.map(node => [node.id, node.data.label] as const), ...(plan?.operations ?? []).map(op => [op.nodeId, op.name] as const)].map(pair => pair)).entries()];
    return <section className="scenario-execution-panel">
    <div className="form-grid"><Field label={"执行方式"}><Select aria-label="场景执行方式" value={mode} disabled={busy} onChange={(selectedValue) => setMode(selectedValue as ScenarioExecutionMode)} popupMatchSelectWidth={true}><Select.Option value="install">完整安装{revision.state !== 'released' ? '测试' : ''}</Select.Option>{revision.sourceRevisionId && <Select.Option value="upgrade">升级{revision.state !== 'released' ? '测试' : '已有集群'}</Select.Option>}<Select.Option value="baseline_verify">恢复后基线复核</Select.Option></Select></Field><Field label={"目标环境"}><Select aria-label="场景执行环境" value={environmentId} disabled={busy} onChange={(selectedValue) => setEnvironmentId(selectedValue)} popupMatchSelectWidth={true}><Select.Option value="">请选择环境</Select.Option>{environments.map(environment => <Select.Option key={environment.id} value={environment.id} disabled={Boolean(environmentIssues?.(environment).length)}>{environment.name}{environmentIssues?.(environment).length ? ' · 适配不匹配' : ''}</Select.Option>)}</Select></Field></div>
    <InfoNote title={MODE_LABELS[mode]}>{mode === 'install' ? `${testOnly ? '安装测试' : '完整安装'}要求干净环境，完成所有组件验证和场景业务验收后才算成功。` : mode === 'upgrade' ? '环境必须实际处于正式来源版本；只执行变化项，随后验证完整目标集群和业务。失败立即停止并保留现场。' : '仅确认恢复后的实际基线，不能代替安装、升级测试，也不会将测试集群转为正式部署。'}</InfoNote>
    {revision.sourceRevisionId && <dl className="scenario-source-facts"><div><dt>升级来源版本</dt><dd>{revision.sourceRevisionId}</dd></div><div><dt>来源正式 Run</dt><dd>{revision.sourceRunId ?? '待补齐'}</dd></div></dl>}
    {mode === 'upgrade' && <section className="scenario-upgrade-order"><h4>升级顺序约束</h4><p>自动依赖确定必要顺序；存在多个可执行项时，补充先后约束后重新预览。</p>{constraints.map((edge, index) => <div className="scenario-inline-actions" key={edge.id}><span>{allNodes.find(([id]) => id === edge.source)?.[1] ?? edge.source} → {allNodes.find(([id]) => id === edge.target)?.[1] ?? edge.target}</span>{editable && <Button className="button button--quiet" aria-label={`删除升级顺序 ${index + 1}`} disabled={busy} onClick={() => { setConstraints(items => items.filter((_, i) => i !== index)); setConstraintsDirty(true); setPlan(undefined); }} htmlType={"button"} type="default"><Trash2 size={15}/></Button>}</div>)}{editable && <><div className="scenario-inline-actions"><Field label={"先执行节点"}><Select value={from} onChange={(selectedValue) => setFrom(selectedValue)} popupMatchSelectWidth={true}><Select.Option value="">请选择</Select.Option>{allNodes.map(([id, name]) => <Select.Option key={id} value={id}>{name}</Select.Option>)}</Select></Field><Field label={"后执行节点"}><Select value={to} onChange={(selectedValue) => setTo(selectedValue)} popupMatchSelectWidth={true}><Select.Option value="">请选择</Select.Option>{allNodes.map(([id, name]) => <Select.Option key={id} value={id}>{name}</Select.Option>)}</Select></Field><Button className="button button--secondary" disabled={busy || !from || !to || from === to || constraints.some(edge => edge.source === from && edge.target === to)} onClick={() => { setConstraints(items => [...items, { id: `upgrade:${from}:${to}`, source: from, target: to, kind: 'sequence' }]); setConstraintsDirty(true); setPlan(undefined); }} htmlType={"button"} type="default"><Plus size={15}/> 增加约束</Button></div><Button className="button button--secondary" disabled={busy || !constraintsDirty} onClick={() => void saveConstraints()} htmlType={"button"} type="default">保存升级顺序</Button></>}</section>}
    <ExecutionPreparationPanel request={{ kind: 'scenario_execution', subjectId: revision.id, environmentId, scenario: { environmentId, executionMode: mode, testOnly } }} disabled={busy || constraintsDirty || !!blockedReason} onPlan={value => { setPlan(value as ScenarioExecutionPreview | undefined); if (value)
        setAttemptId(newScenarioClientID()); }}/>
    <ModalFooter>{canLaunch && <Button className="button button--primary" disabled={busy || !plan?.ready || constraintsDirty || !!blockedReason} onClick={() => void execute()} htmlType={"button"} type="primary"><Beaker size={15}/> {testOnly ? `提交${mode === 'upgrade' ? '升级' : '安装'}测试` : mode === 'baseline_verify' ? '开始基线复核' : mode === 'upgrade' ? '提交正式升级' : '提交正式安装'}</Button>}</ModalFooter>
    {blockedReason && <p className="inline-warning" role="alert">{blockedReason}</p>}
    <StatusExplanationPanel explanation={explanation} title="场景执行被阻断"/>
    {plan && <div className="scenario-execution-preview"><header><strong>{plan.ready ? '计划已就绪' : '计划存在阻塞'}</strong>{plan.baselineRunId && <span>环境基线 Run：{plan.baselineRunId}</span>}</header>{plan.needsApproval && <p className="inline-warning">本次执行需经环境 Owner 审批，提交后进入审批队列。</p>}<ScenarioEvidence revision={revision} installationTest={plan.installationTest} upgradeTest={plan.upgradeTest}/>{plan.issues.map((issue, index) => <p className="inline-warning" role="alert" key={`${issue.code}-${index}`}>{issue.nodeId ? `${allNodes.find(([id]) => id === issue.nodeId)?.[1] ?? issue.nodeId}：` : ''}{issue.message}</p>)}
      {plan.operations.length > 0 && <div className="table-wrap"><Table dataSource={plan.operations} rowKey={op => op.nodeId + '-' + op.change} pagination={false} scroll={{x: 680}} columns={[
{title:'节点',dataIndex:'name',key:'name'}, {title:'变化',key:'change',render:(_,op)=>CHANGE_LABELS[op.change] ?? op.change},
{title:'来源 Release',key:'from',render:(_,op)=>op.fromReleaseId ?? '—'}, {title:'目标 Release',key:'to',render:(_,op)=>op.toReleaseId ?? '—'}
]} /></div>}
      {plan.steps.length > 0 && <ol className="scenario-execution-steps">{plan.steps.map((step, index) => <li key={`${step.nodeId}-${index}`}><strong>{(step.name || step.nodeId || `步骤 ${index + 1}`).replace(/source_verify|target_verify/g, stage => STAGE_LABELS[stage])}</strong><span>{STAGE_LABELS[step.phase ?? ''] ?? step.phase} · {ACTION_LABELS[step.action ?? ''] ?? step.action} · {step.hostGroup}</span></li>)}</ol>}
    </div>}
  </section>;
}
export function ScenarioEvidence({ revision, installationTest = revision.installationTest, upgradeTest = revision.upgradeTest }: {
    revision: ScenarioRevision;
    installationTest?: ScenarioRevision['installationTest'];
    upgradeTest?: ScenarioRevision['upgradeTest'];
}) {
    return <div className="scenario-evidence" aria-label="场景发布测试证据">{[
            { label: '安装测试', evidence: installationTest, optional: false },
            { label: '升级测试', evidence: upgradeTest, optional: !revision.sourceRevisionId },
        ].map(({ label, evidence, optional }) => {
            const Icon = optional ? Minus : evidence?.valid ? CheckCircle2 : CircleDashed;
            return <div key={label} className={optional ? 'is-neutral' : evidence?.valid ? 'is-valid' : 'is-pending'}>
      <span className="scenario-evidence__icon"><Icon size={18} aria-hidden="true"/></span>
      <div className="scenario-evidence__content"><strong>{label}</strong><span>{optional ? '首版无需升级测试' : evidence?.valid ? '有效' : evidence?.reason ?? '尚无有效证据'}</span></div>
      {evidence?.runId && <a href={`/runs?selected=${encodeURIComponent(evidence.runId)}`}>查看 Run <ArrowUpRight size={13} aria-hidden="true"/></a>}
    </div>;
        })}</div>;
}
