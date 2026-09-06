import { useEffect, useState } from 'react';
import { api } from '../api/client';
import { displayError, useApp } from '../context/AppContext';
import type { Run } from '../types/domain';
import { Download, FileCheck2, Package, ShieldCheck } from 'lucide-react';
import { InfoNote, StatusPill } from './Primitives';
export function RunDeliveryPanel({run}:{run:Run}){
 const {user}=useApp();const [value,setValue]=useState<Awaited<ReturnType<typeof api.verifiedJobEligibility>>>();const [error,setError]=useState('');
 useEffect(()=>{setValue(undefined);setError('');if(!run.jobDigest)return;const controller=new AbortController();void api.verifiedJobEligibility(run.id,controller.signal).then(setValue).catch(reason=>{if(!controller.signal.aborted)setError(displayError(reason))});return()=>controller.abort()},[run.id,run.status,run.jobDigest,user.id]);
 if(!run.jobDigest)return null;
 return <article className="panel execution-delivery">
  <header className="panel__header"><div><span className="panel__icon"><Package size={18} aria-hidden="true" /></span><div><h2>作业交付</h2><p>查看下载范围与验证依据，选择需要交付的作业包。</p></div></div></header>
  <div className="delivery-options">
   <section className="delivery-card"><header><FileCheck2 size={18} aria-hidden="true" /><h3>本次作业包</h3><StatusPill status="locked">已锁定</StatusPill></header><p className="delivery-scope">{run.retryOfRunId?'本次续跑选择的阶段':'本次锁定的执行范围'} · {run.purposeCounts?.total??run.steps?.length??0} 步</p><a className="button button--secondary" href={api.runJobDownloadURL(run.id)}><Download size={14} aria-hidden="true" />下载本次作业包</a></section>
   <section className={`delivery-card${value?.eligible ? ' delivery-card--verified' : ''}`}><header><ShieldCheck size={18} aria-hidden="true" /><h3>已验证完整作业</h3><StatusPill status={value?.eligible ? 'succeeded' : error ? 'failed' : 'pending'}>{value?.eligible ? '可交付' : error ? '核验失败' : value ? '待满足条件' : '核验中'}</StatusPill></header>{value?.eligible?<><p className="delivery-scope">根作业：{value.rootRunId} · 完整流程 {value.stageCount} 步</p><p className="delivery-evidence">证据链：{value.evidenceRunIds.join(' → ')}</p><a className="button button--primary" href={api.runJobDownloadURL(run.id,true)}><Download size={14} aria-hidden="true" />下载已验证完整作业</a></>:<div role="status"><InfoNote>{error||value?.reason||'正在核验完整流程、合同和证据链…'}</InfoNote></div>}</section>
  </div>
 </article>
}
