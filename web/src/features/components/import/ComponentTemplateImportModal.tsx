import { Table } from 'antd';
import { useDialogs } from '../../../components/UIProvider';
import { Input, Button } from 'antd';
import { Upload } from 'lucide-react';
import { useState } from 'react';
import { api } from '../../../api/client';
import { Modal } from '../../../components/Primitives';
import { displayError, useApp } from '../../../context/AppContext';
import { parseComponentImportTemplate } from './parseComponentTemplate';
export function ComponentTemplateImportModal({ onClose, onDone }: {
    onClose: () => void;
    onDone: () => void;
}) {
    const { confirm } = useDialogs();
    const { notify } = useApp();
    const [text, setText] = useState('[\n  {\n    "component": {\n      "name": "示例组件",\n      "slug": "example-component",\n      "description": "单一职责说明",\n      "layer": "orchestration_core",\n      "tags": ["worker", "core"]\n    },\n    "release": {\n      "version": "1.0.0",\n      "releaseNotes": "初始细粒度版本",\n      "environmentConstraints": {\n        "architecture": ["amd64"],\n        "operatingSystem": ["Ubuntu"],\n        "operatingSystemVersion": ["18.04"]\n      },\n      "parameters": [],\n      "dependencies": [],\n      "actions": []\n    },\n    "playbooks": []\n  }\n]');
    const [busy, setBusy] = useState(false);
    const [progress, setProgress] = useState('');
    const [importResult, setImportResult] = useState<Awaited<ReturnType<typeof api.importComponents>>>();
    async function runImport() {
        setBusy(true);
        try {
            const entries = parseComponentImportTemplate(text);
            setProgress('服务端正在完整预检组件、依赖和 Playbook…');
            const plan = await api.previewComponentImport(entries);
            if (!await confirm(`导入预览\n${plan.order.length} 个组件\n顺序：${plan.order.join(' → ')}\n\n确认按此计划导入？`))
                return;
            setProgress('服务端正在原子写入组件、Draft 和 Playbook…');
            const result = await api.importComponents(entries, plan.planDigest);
            notify('success', `已原子录入 ${result.completedComponents.length} 个细粒度组件`, '服务端已复核计划指纹；版本仍为 Draft，可继续验证并加入候选发布集。');
            setImportResult(result);
        }
        catch (reason) {
            notify('error', '组件模板导入失败', displayError(reason));
        }
        finally {
            setBusy(false);
            setProgress('');
        }
    }
    return <Modal busy={Boolean(busy)} size="wide" title="批量导入组件" description="服务端先完整预检且不写入；确认计划指纹后原子创建全部组件、Draft 和独立 Playbook，任一失败则整批不生效。" onClose={onClose} footer={<footer className="modal-actions"><Button className="button button--quiet" disabled={busy} onClick={onClose} htmlType={"button"} type="default">取消</Button>{importResult ? <Button className="button button--primary" onClick={onDone} htmlType={"button"} type="primary">完成并查看组件</Button> : <Button className="button button--primary" disabled={busy} onClick={() => void runImport()} htmlType={"button"} type="primary"><Upload size={16}/> {busy ? '处理中…' : '预检并导入'}</Button>}</footer>}><div className="modal-body">{importResult ? <section className="import-result"><h3>已生成 {importResult.completedComponents.length} 个组件 Draft</h3><p>后续编辑和同步使用当前入口路径。未引用的原副本可在工作区检查后删除。</p><div className="table-wrap"><Table dataSource={importResult.fileMappings ?? []} rowKey={m => m.currentPath} pagination={false} scroll={{x: 760}} columns={[
{title:'原文件路径',dataIndex:'originalPath',key:'original'}, {title:'当前入口路径',dataIndex:'currentPath',key:'current'}, {title:'动作',key:'action',render:(_,m)=>m.actionId || '静态文件'}
]} /></div></section> : <Input.TextArea aria-label="组件模板 JSON" className="code-editor" rows={22} value={text} disabled={busy} onChange={(event) => setText(event.target.value)} spellCheck={false}/>}{progress && <div className="inline-warning"><span>{progress}</span></div>}</div></Modal>;
}
