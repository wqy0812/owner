import { Button, Input, Select, Checkbox } from 'antd';
import { useState } from 'react';
import { Field } from '../../../components/Field';
import { useApp } from '../../../context/AppContext';
import type { ActionDefinition, ComponentRelease } from '../../../types/domain';
import { ACTION_OPTIONS, splitCSV } from '../model';

function CredentialNameEditor({ values, onChange }: {
    values: string[];
    onChange: (values: string[]) => void;
}) {
    const [input, setInput] = useState('');
    function add() {
        const additions = splitCSV(input);
        if (!additions.length)
            return;
        onChange([...new Set([...values, ...additions])]);
        setInput('');
    }
    return <div className="span-2 credential-ref-editor">
    <div className="credential-ref-editor__label"><span>所需 CredentialRef</span>{values.length ? <button type="button" onClick={() => onChange([])}>清空全部</button> : null}</div>
    <div className="credential-ref-tags" aria-label="所需 CredentialRef 列表">
      {values.map((name) => <span key={name}>{name}<button type="button" aria-label={`删除 CredentialRef ${name}`} onClick={() => onChange(values.filter((item) => item !== name))}>×</button></span>)}
      {!values.length ? <small>未声明 CredentialRef</small> : null}
    </div>
    <div className="inline-field"><Input aria-label="添加 CredentialRef" value={input} onChange={(event) => setInput(event.target.value)} onKeyDown={(event) => {
            if (event.key === 'Enter' || event.key === ',') {
                    event.preventDefault();
                add();
            }
        }} placeholder="输入名称后按回车"/><Button className="button button--quiet" disabled={!input.trim()} onClick={add} htmlType={"button"} type="default">添加</Button></div>
  </div>;
}
export function PlaybookActionSettings({ action, actions, releases, updateAction, onTypeChange, disabled }: {
    disabled: boolean;
    action: ActionDefinition;
    actions: ActionDefinition[];
    releases: ComponentRelease[];
    updateAction: (patch: Partial<ActionDefinition>) => void;
    onTypeChange: () => void;
}) {
    const { platformOptionCategories } = useApp();
    const hostGroups = platformOptionCategories.find(category => category.kind === 'host_group')?.options ?? [];
    const releaseIds = new Set(releases.map(release => release.id));
    return <aside className="playbook-settings" aria-label="当前动作配置">
      <header><h4>动作配置</h4><span>与当前 YAML 一起保存</span></header>
      <fieldset disabled={disabled} className="form-grid playbook-action-fields">
        <Field label={"动作类型"} extra={<>{action.id ? <small>已保存动作的类型不可修改；如需更换，请删除后新建。</small> : null}</>}><Select value={action.type} disabled={Boolean(action.id)} onChange={(selectedValue) => {
                const type = selectedValue as ActionDefinition['type'];
                onTypeChange();
                updateAction({ type, name: !action.name || action.name === action.type ? type : action.name, idempotent: type !== 'check' ? action.idempotent : false, preCheckActionId: type === 'rollback' || type === 'check' ? '' : action.preCheckActionId });
            }} popupMatchSelectWidth={true}>{ACTION_OPTIONS.map((option) => <Select.Option key={option.value} value={option.value}>{option.value} · {option.label}</Select.Option>)}</Select></Field>
        <Field label={"动作名称"}><Input aria-label="动作名称" value={action.name ?? ''} onChange={event => updateAction({ name: event.target.value })}/></Field><Field label={<>{action.type === 'check' ? '独立测试主机组（绑定时继承执行动作）' : '目标主机组'}</>} required={action.type !== 'check'}><Select value={action.hostGroup ?? ''} onChange={(selectedValue) => updateAction({ hostGroup: selectedValue })} popupMatchSelectWidth={true}><Select.Option value="">请选择主机组</Select.Option>{hostGroups.map((option) => <Select.Option key={option.id} value={option.value}>{option.label}</Select.Option>)}</Select></Field>
        {action.type !== 'check' && <>
          {action.type !== 'rollback' && <Field label={"前置检查 *"}><Select aria-label="前置检查" value={action.preCheckActionId ?? ''} onChange={(selectedValue) => updateAction({ preCheckActionId: selectedValue })} popupMatchSelectWidth={true}><Select.Option value="">请选择本版本的检查动作</Select.Option>{actions.filter(item => item.type === 'check' && item.id).map(item => <Select.Option key={item.id} value={item.id}>{item.name || item.id}</Select.Option>)}</Select></Field>}
          <Field label={<>{action.type === 'rollback' ? '回滚后检查' : '后置检查 *'}</>}><Select aria-label="后置检查" value={action.postCheckActionId ?? ''} onChange={(selectedValue) => updateAction({ postCheckActionId: selectedValue })} popupMatchSelectWidth={true}><Select.Option value="">{action.type === 'rollback' ? '复用被回滚动作的前置检查' : '请选择本版本的检查动作'}</Select.Option>{actions.filter(item => item.type === 'check' && item.id).map(item => <Select.Option key={item.id} value={item.id}>{item.name || item.id}</Select.Option>)}</Select></Field>
          <Field label={"可安全重试"} extra={<><small>{action.type === 'rollback' ? '仅在已验证部分执行后可安全重跑时声明；回滚主体成功后只重试未通过的后检查。' : '仅在已验证部分执行后可安全重跑时声明；已绑定的前置检查会随动作重试。'}</small></>}><Select aria-label="可安全重试" value={String(action.idempotent ?? false)} onChange={(selectedValue) => updateAction({ idempotent: selectedValue === 'true' })} popupMatchSelectWidth={true}><Select.Option value="false">否 · 未声明可安全重试</Select.Option><Select.Option value="true">是 · 已验证可安全重试</Select.Option></Select></Field>
          {action.type === 'rollback' && !action.postCheckActionId && <p className="info-note span-2">回滚后按实际来源动作复用检查：{actions.filter(item => ['install', 'configure', 'upgrade'].includes(item.type)).map(item => `${item.name || item.type} → ${actions.find(check => check.id === item.preCheckActionId)?.name || '待绑定'}`).join('；')}。执行计划将展示具体检查 YAML。</p>}
          {action.type !== 'rollback' && (!action.preCheckActionId || !action.postCheckActionId) && <p className="form-validation span-2">此动作尚未绑定完整检查，可保存 Draft，绑定完成后才能执行或发布。</p>}
        </>}
        <Checkbox className="checkbox-field" checked={action.become ?? false} onChange={(event) => updateAction({ become: event.target.checked })}><span>提权执行</span></Checkbox>
        <Field label={"超时（秒）"}><Input type="number" min={1} value={action.timeoutSeconds ?? 1800} onChange={(event) => updateAction({ timeoutSeconds: Number(event.target.value) })}/></Field>
        <Field label={"风险级别"}><Select value={action.riskLevel ?? 'low'} onChange={(selectedValue) => { const riskLevel = selectedValue as NonNullable<ActionDefinition['riskLevel']>; updateAction({ riskLevel, destructive: riskLevel === 'destructive' }); }} popupMatchSelectWidth={true}><Select.Option value="low">低</Select.Option><Select.Option value="medium">中</Select.Option><Select.Option value="high">高</Select.Option><Select.Option value="destructive">破坏性（需审批）</Select.Option></Select></Field>

        <p className="info-note span-2">{action.type === 'rollback' ? '回滚直接执行恢复 YAML，再由回滚后检查验证恢复结果；需要的 facts 由 YAML 自行采集。' : 'facts 由本动作 YAML 自行采集；运行条件与残留探测写入前置检查 YAML，在依赖完成后执行。'}</p>
        <Field label={"Tags（逗号分隔）"}><Input value={(action.tags ?? []).join(', ')} onChange={(event) => updateAction({ tags: splitCSV(event.target.value) })}/></Field>
        <CredentialNameEditor values={action.requiredCredentials ?? []} onChange={(requiredCredentials) => updateAction({ requiredCredentials })}/>
        {(action.type === 'upgrade' || action.type === 'rollback') ? <>
          <Field label={"来源版本"}><Select aria-label="来源 Release" value={action.fromReleaseId ?? ''} onChange={(selectedValue) => updateAction({ fromReleaseId: selectedValue || undefined })} popupMatchSelectWidth={true}><Select.Option value="">请选择来源版本</Select.Option>{action.fromReleaseId && !releaseIds.has(action.fromReleaseId) ? <Select.Option value={action.fromReleaseId}>{action.fromReleaseId} · 现有值</Select.Option> : null}{releases.map((item) => <Select.Option key={item.id} value={item.id}>{item.version} · {item.id}</Select.Option>)}</Select></Field>
          <Field label={"目标版本"}><Select aria-label="目标 Release" value={action.toReleaseId ?? ''} onChange={(selectedValue) => updateAction({ toReleaseId: selectedValue || undefined })} popupMatchSelectWidth={true}><Select.Option value="">请选择目标版本</Select.Option>{action.toReleaseId && !releaseIds.has(action.toReleaseId) ? <Select.Option value={action.toReleaseId}>{action.toReleaseId} · 现有值</Select.Option> : null}{releases.map((item) => <Select.Option key={item.id} value={item.id}>{item.version} · {item.id}</Select.Option>)}</Select></Field>
          {action.type === 'rollback' ? <small className="span-2">起止版本都留空时表示回退当前版本的安装；填写时表示从当前版本回到指定旧版本。</small> : null}
        </> : null}
      </fieldset>
    </aside>;
}
