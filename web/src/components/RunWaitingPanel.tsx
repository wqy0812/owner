import type { Run, RunWaitingObservation } from '../types/domain';
import { Clock3, Radio, Server } from 'lucide-react';
import { InfoNote, StatusPill } from './Primitives';
export function RunWaitingPanel({status, observations}:{status:Run['status'];observations:RunWaitingObservation[]}){
 if(status!=='running')return null;
 return <article className="panel execution-waiting">
  <header className="panel__header"><div><span className="panel__icon panel__icon--cyan"><Radio size={18} aria-hidden="true" /></span><div><h2>等待观测</h2><p>对照期望状态与最新观测，查看当前等待进展。</p></div></div><StatusPill status="running">{observations.length ? `${observations.length} 项等待` : '等待上报'}</StatusPill></header>
  <div className="waiting-observations">{observations.length?observations.map((o,i)=><section className="waiting-observation" key={i}>
   <header><strong>{o.waiting.object||o.task}</strong><span><Server size={13} aria-hidden="true" />{o.host}</span></header>
   <dl className="observation-facts"><div><dt>期望</dt><dd>{o.waiting.expected||'未上报'}</dd></div><div><dt>最新观测</dt><dd>{o.waiting.observed||'未上报'}</dd></div></dl>
   <footer><span className="editor-count">尝试 {o.waiting.attempt??0} 次</span><span><Clock3 size={13} aria-hidden="true" />截止时间：{o.waiting.deadline?new Date(o.waiting.deadline).toLocaleString():'未上报（仍受本阶段超时限制）'}</span></footer>
  </section>):<InfoNote title="等待观测上报">等待对象与观测值尚未上报；阶段仍受锁定计划中的超时上限约束。</InfoNote>}</div>
 </article>
}
