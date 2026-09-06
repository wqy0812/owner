import {useApp} from '../context/AppContext';
export function useHostGroupLabel(){const {platformOptionCategories}=useApp();const options=(platformOptionCategories??[]).find(c=>c.kind==='host_group')?.options??[];return (value?:string)=>{if(!value)return '未指定主机组';const option=options.find(o=>o.value===value);if(!option)return value==='all'?'全部主机':'无法匹配主机组';return option.label+(option.retiredAt?' · 已退役':'')}}
export function HostGroupName({value}:{value?:string}){const label=useHostGroupLabel();return <span title={value?`技术 ID：${value}`:undefined}>{label(value)}</span>}
