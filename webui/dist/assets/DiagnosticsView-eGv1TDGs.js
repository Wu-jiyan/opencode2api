import{T as H}from"./Select-DgUSKz0V.js";import{q as X,s as L,v as x,x as o,y as Y,z as E,D as U,E as ee,G as te,H as J,I as Q,J as oe,b as u,c as h,K as g,L as k,M as p,d as _,N as re,O as le,P as ne,Q as ae,R as T,S as se,T as G,u as ie,w as de,o as ce,j as M,f as S,g as $,F as pe,r as ue,e as be,t as me,C as q,U as ge,B as he,h as fe,l as ve,k as W,n as V,p as ye}from"./index-BlAFkLXC.js";import{D as xe}from"./DataTable-DpdTQeBF.js";var _e={thPaddingBorderedSmall:"8px 12px",thPaddingBorderedMedium:"12px 16px",thPaddingBorderedLarge:"16px 24px",thPaddingSmall:"0",thPaddingMedium:"0",thPaddingLarge:"0",tdPaddingBorderedSmall:"8px 12px",tdPaddingBorderedMedium:"12px 16px",tdPaddingBorderedLarge:"16px 24px",tdPaddingSmall:"0 0 8px 0",tdPaddingMedium:"0 0 12px 0",tdPaddingLarge:"0 0 16px 0"};function Ce(l){const{tableHeaderColor:b,textColor2:s,textColor1:a,cardColor:n,modalColor:i,popoverColor:f,dividerColor:d,borderRadius:c,fontWeightStrong:v,lineHeight:C,fontSizeSmall:r,fontSizeMedium:w,fontSizeLarge:z}=l;return{..._e,lineHeight:C,fontSizeSmall:r,fontSizeMedium:w,fontSizeLarge:z,titleTextColor:a,thColor:L(n,b),thColorModal:L(i,b),thColorPopover:L(f,b),thTextColor:a,thFontWeight:v,tdTextColor:s,tdColor:n,tdColorModal:i,tdColorPopover:f,borderColor:L(n,d),borderColorModal:L(i,d),borderColorPopover:L(f,d),borderRadius:c}}const we={common:X,self:Ce};function K(l,b="default",s=[]){const{children:a}=l;if(a!==null&&typeof a=="object"&&!Array.isArray(a)){const n=a[b];if(typeof n=="function")return n()}return s}var Se=x([o("descriptions",{fontSize:"var(--n-font-size)"},[o("descriptions-separator",`
 display: inline-block;
 margin: 0 8px 0 2px;
 `),o("descriptions-table-wrapper",[o("descriptions-table",[o("descriptions-table-row",[o("descriptions-table-header",{padding:"var(--n-th-padding)"}),o("descriptions-table-content",{padding:"var(--n-td-padding)"})])])]),Y("bordered",[o("descriptions-table-wrapper",[o("descriptions-table",[o("descriptions-table-row",[x("&:last-child",[o("descriptions-table-content",{paddingBottom:0})])])])])]),E("left-label-placement",[o("descriptions-table-content",[x("> *",{verticalAlign:"top"})])]),E("left-label-align",[x("th",{textAlign:"left"})]),E("center-label-align",[x("th",{textAlign:"center"})]),E("right-label-align",[x("th",{textAlign:"right"})]),E("bordered",[o("descriptions-table-wrapper",`
 border-radius: var(--n-border-radius);
 overflow: hidden;
 background: var(--n-merged-td-color);
 border: 1px solid var(--n-merged-border-color);
 `,[o("descriptions-table",[o("descriptions-table-row",[x("&:not(:last-child)",[o("descriptions-table-content",{borderBottom:"1px solid var(--n-merged-border-color)"}),o("descriptions-table-header",{borderBottom:"1px solid var(--n-merged-border-color)"})]),o("descriptions-table-header",`
 font-weight: 400;
 background-clip: padding-box;
 background-color: var(--n-merged-th-color);
 `,[x("&:not(:last-child)",{borderRight:"1px solid var(--n-merged-border-color)"})]),o("descriptions-table-content",[x("&:not(:last-child)",{borderRight:"1px solid var(--n-merged-border-color)"})])])])])]),o("descriptions-header",`
 font-weight: var(--n-th-font-weight);
 font-size: 18px;
 transition: color .3s var(--n-bezier);
 line-height: var(--n-line-height);
 margin-bottom: 16px;
 color: var(--n-title-text-color);
 `),o("descriptions-table-wrapper",`
 transition:
 background-color .3s var(--n-bezier),
 border-color .3s var(--n-bezier);
 `,[o("descriptions-table",`
 width: 100%;
 border-collapse: separate;
 border-spacing: 0;
 box-sizing: border-box;
 `,[o("descriptions-table-row",`
 box-sizing: border-box;
 transition: border-color .3s var(--n-bezier);
 `,[o("descriptions-table-header",`
 font-weight: var(--n-th-font-weight);
 line-height: var(--n-line-height);
 display: table-cell;
 box-sizing: border-box;
 color: var(--n-th-text-color);
 transition:
 color .3s var(--n-bezier),
 background-color .3s var(--n-bezier),
 border-color .3s var(--n-bezier);
 `),o("descriptions-table-content",`
 vertical-align: top;
 line-height: var(--n-line-height);
 display: table-cell;
 box-sizing: border-box;
 color: var(--n-td-text-color);
 transition:
 color .3s var(--n-bezier),
 background-color .3s var(--n-bezier),
 border-color .3s var(--n-bezier);
 `,[U("content",`
 transition: color .3s var(--n-bezier);
 display: inline-block;
 color: var(--n-td-text-color);
 `)]),U("label",`
 font-weight: var(--n-th-font-weight);
 transition: color .3s var(--n-bezier);
 display: inline-block;
 margin-right: 14px;
 color: var(--n-th-text-color);
 `)])])])]),o("descriptions-table-wrapper",`
 --n-merged-th-color: var(--n-th-color);
 --n-merged-td-color: var(--n-td-color);
 --n-merged-border-color: var(--n-border-color);
 `),ee(o("descriptions-table-wrapper",`
 --n-merged-th-color: var(--n-th-color-modal);
 --n-merged-td-color: var(--n-td-color-modal);
 --n-merged-border-color: var(--n-border-color-modal);
 `)),te(o("descriptions-table-wrapper",`
 --n-merged-th-color: var(--n-th-color-popover);
 --n-merged-td-color: var(--n-td-color-popover);
 --n-merged-border-color: var(--n-border-color-popover);
 `))]);const ze="DESCRIPTION_ITEM_FLAG";function Pe(l){return typeof l=="object"&&l&&!Array.isArray(l)?l.type&&l.type.DESCRIPTION_ITEM_FLAG:!1}const ke=["colspan"],$e=["colspan"],Te=["colspan"],Re=["colspan"],Be={...Q.props,title:String,column:{type:Number,default:3},columns:Number,labelPlacement:{type:String,default:"top"},labelAlign:{type:String,default:"left"},separator:{type:String,default:":"},size:String,bordered:Boolean,labelClass:String,labelStyle:[Object,String],contentClass:String,contentStyle:[Object,String]};var Le=J({name:"Descriptions",props:Be,slots:Object,setup(l){const{mergedClsPrefixRef:b,inlineThemeDisabled:s,mergedComponentPropsRef:a}=ne(l),n=T(()=>l.size||a?.value?.Descriptions?.size||"medium"),i=Q("Descriptions","-descriptions",Se,we,l,b),f=T(()=>{const{bordered:c}=l,v=n.value,{common:{cubicBezierEaseInOut:C},self:{titleTextColor:r,thColor:w,thColorModal:z,thColorPopover:P,thTextColor:D,thFontWeight:e,tdTextColor:t,tdColor:y,tdColorModal:m,tdColorPopover:R,borderColor:I,borderColorModal:A,borderColorPopover:B,borderRadius:N,lineHeight:O,[G("fontSize",v)]:j,[G(c?"thPaddingBordered":"thPadding",v)]:F,[G(c?"tdPaddingBordered":"tdPadding",v)]:Z}}=i.value;return{"--n-title-text-color":r,"--n-th-padding":F,"--n-td-padding":Z,"--n-font-size":j,"--n-bezier":C,"--n-th-font-weight":e,"--n-line-height":O,"--n-th-text-color":D,"--n-td-text-color":t,"--n-th-color":w,"--n-th-color-modal":z,"--n-th-color-popover":P,"--n-td-color":y,"--n-td-color-modal":m,"--n-td-color-popover":R,"--n-border-radius":N,"--n-border-color":I,"--n-border-color-modal":A,"--n-border-color-popover":B}}),d=s?ae("descriptions",T(()=>{let c="";const{bordered:v}=l;return v&&(c+="a"),c+=n.value[0],c}),f,l):void 0;return{mergedClsPrefix:b,cssVars:s?void 0:f,themeClass:d?.themeClass,onRender:d?.onRender,compitableColumn:se(l,["columns","column"]),inlineThemeDisabled:s,mergedSize:n}},render(){const l=this.$slots.default,b=l?oe(l()):[];b.length;const{contentClass:s,labelClass:a,compitableColumn:n,labelPlacement:i,labelAlign:f,mergedSize:d,bordered:c,title:v,cssVars:C,mergedClsPrefix:r,separator:w,onRender:z}=this;z?.();const P=b.filter(e=>Pe(e)),D=P.reduce((e,t,y)=>{const m=t.props||{},R=P.length-1===y,I=["label"in m?m.label:K(t,"label")],A=[K(t)],B=m.span||1,N=e.span;e.span+=B;const O=m.labelStyle||m["label-style"]||this.labelStyle,j=m.contentStyle||m["content-style"]||this.contentStyle;if(i==="left")c?e.row.push((u(),h("th",{key:1,class:p([`${r}-descriptions-table-header`,a]),colspan:1,style:k(O)},[g(()=>I)],6)),(u(),h("td",{key:2,class:p([`${r}-descriptions-table-content`,s]),colspan:R?(n-N)*2+1:B*2-1,style:k(j)},[g(()=>A)],14,ke))):e.row.push((u(),h("td",{key:3,class:p(`${r}-descriptions-table-content`),colspan:R?(n-N)*2:B*2},[_("span",{class:p([`${r}-descriptions-table-content__label`,a]),style:k(O)},[g(()=>[...I,w&&(u(),h("span",{key:4,class:p(`${r}-descriptions-separator`)},[g(()=>w)],2))])],6),_("span",{class:p([`${r}-descriptions-table-content__content`,s]),style:k(j)},[g(()=>A)],6)],10,$e)));else{const F=R?(n-N)*2:B*2;e.row.push((u(),h("th",{key:5,class:p([`${r}-descriptions-table-header`,a]),colspan:F,style:k(O)},[g(()=>I)],14,Te))),e.secondRow.push((u(),h("td",{key:6,class:p([`${r}-descriptions-table-content`,s]),colspan:F,style:k(j)},[g(()=>A)],14,Re)))}return(e.span>=n||R)&&(e.span=0,e.row.length&&(e.rows.push(e.row),e.row=[]),i!=="left"&&e.secondRow.length&&(e.rows.push(e.secondRow),e.secondRow=[])),e},{span:0,row:[],secondRow:[],rows:[]}).rows.map(e=>(u(),h("tr",{class:p(`${r}-descriptions-table-row`)},[g(()=>e)],2)));return u(),h("div",{style:k(C),class:p([`${r}-descriptions`,this.themeClass,`${r}-descriptions--${i}-label-placement`,`${r}-descriptions--${f}-label-align`,`${r}-descriptions--${d}-size`,c&&`${r}-descriptions--bordered`])},[v||this.$slots.header?(u(),h("div",{key:0,class:p(`${r}-descriptions-header`)},[g(()=>v||re(this,"header"))],2)):g(()=>null),_("div",{class:p(`${r}-descriptions-table-wrapper`)},[_("table",{class:p(`${r}-descriptions-table`)},[_("tbody",null,[g(()=>i==="top"&&(u(),h("tr",{class:p(`${r}-descriptions-table-row`),style:{visibility:"collapse"}},[g(()=>le(n*2,(u(),h("td"))))],2))),g(()=>D)])],2)],2)],6)}});const Me={label:String,span:{type:Number,default:1},labelClass:String,labelStyle:[Object,String],contentClass:String,contentStyle:[Object,String]};var De=J({name:"DescriptionsItem",[ze]:!0,props:Me,slots:Object,render(){return null}});const Ie={class:"mono"},Ae={class:"card-toolbar"},Ee={__name:"DiagnosticsView",props:{reloadSignal:{type:Number,default:0}},setup(l){const b=l,s=ie(),a=W(!1),n=W(!1),i=W(null),f=W(""),d=T(()=>i.value?.metadata||{}),c=T(()=>i.value?.catalog||{}),v=T(()=>[["元数据就绪",d.value.ready?"是":"否"],["价格模型数",Number(d.value.models||0).toLocaleString()],["更新时间",C(d.value.updated_at)],["是否过期",d.value.stale?"是":"否"],["下次刷新",C(d.value.next_refresh)],["最近错误",d.value.last_error||"无"],["上游模型",`Zen ${c.value.zen||0} · Go ${c.value.go||0}`],["可暴露模型",Number(c.value.exposed||0).toLocaleString()]]);function C(e){if(!e)return"—";const t=new Date(e);return Number.isNaN(t.getTime())?"—":t.toLocaleString()}const r=T(()=>{const e=f.value.trim().toLowerCase();return(i.value?.models||[]).filter(t=>!e||t.model.toLowerCase().includes(e)||(t.alias||"").toLowerCase().includes(e))}),w={name_free:"名称含 free",name_and_metadata_free:"名称与价格均为免费",metadata_free:"价格为零",metadata_paid:"价格为付费",metadata_deprecated:"已弃用",metadata_cost_unknown:"价格未知",metadata_model_missing:"元数据未收录",metadata_pending:"元数据未就绪",name_fallback_metadata_pending:"名称推断（元数据未就绪）"},z=[{title:"模型",key:"model",minWidth:230,render:e=>V("div",null,[V("div",{class:"mono"},e.model),e.alias?V("div",{class:"section-caption"},`对外 ${e.alias}`):null])},{title:"原生协议",key:"protocol",width:170,render:e=>`${e.native_protocol||"—"} · ${e.protocol_source||"—"}`},{title:"路由",key:"route",minWidth:150,render:e=>{if(e.route_error)return V(H,{type:"error",size:"small",bordered:!1,title:e.route_error},{default:()=>"不可路由"});const t=[...e.anonymous?["anonymous"]:[],...e.key_tiers||[]];return t.length?t.join(" → "):"—"}},{title:"匿名资格",key:"anonymous",width:100,render:e=>{const t=e.anonymous_eligibility?.allowed;return V(H,{type:t?"success":"default",size:"small",bordered:!1},{default:()=>t?"允许":"拒绝"})}},{title:"判断来源",key:"source",minWidth:200,ellipsis:{tooltip:!0},render:e=>{const t=e.anonymous_eligibility?.source;return t?w[t]||t:"—"}},{title:"成本 input / output",key:"cost",minWidth:170,render:e=>{const t=e.anonymous_eligibility||{};if(t.input_cost==null&&t.output_cost==null)return"未知";const y=t.input_cost==null?"?":t.input_cost,m=t.output_cost==null?"?":t.output_cost;return`${y} / ${m}`}}];async function P(e=!1){a.value=!0;try{i.value=await ve("/api/debug/models"),e&&s.success("诊断数据已刷新")}catch(t){e&&s.error(t.message)}finally{a.value=!1}}async function D(){n.value=!0;try{i.value=await ye("/api/models/refresh",{}),s.success("已从上游重新拉取模型目录与价格元数据")}catch(e){s.error(e.message)}finally{n.value=!1}}return de(()=>b.reloadSignal,()=>P(!0)),ce(()=>P(!1)),(e,t)=>(u(),h("div",null,[M($(q),{title:"模型元数据",size:"small",bordered:!1,class:"block-gap"},{"header-extra":S(()=>[...t[1]||(t[1]=[_("span",{class:"section-caption"}," 零成本或名称含 free 任一条件满足即可进入匿名通道 ",-1)])]),default:S(()=>[M($(Le),{column:4,"label-placement":"top",size:"small"},{default:S(()=>[(u(!0),h(pe,null,ue(v.value,([y,m])=>(u(),be($(De),{key:y,label:y},{default:S(()=>[_("span",Ie,me(m),1)]),_:2},1032,["label"]))),128))]),_:1})]),_:1}),M($(q),{size:"small",bordered:!1},{header:S(()=>[...t[2]||(t[2]=[_("span",null,"模型路由表",-1)])]),"header-extra":S(()=>[_("div",Ae,[M($(ge),{value:f.value,"onUpdate:value":t[0]||(t[0]=y=>f.value=y),placeholder:"筛选模型",clearable:"",size:"small",style:{width:"220px"}},null,8,["value"]),M($(he),{size:"small",loading:n.value,onClick:D},{default:S(()=>[...t[3]||(t[3]=[fe("刷新",-1)])]),_:1},8,["loading"])])]),default:S(()=>[M($(xe),{columns:z,data:r.value,loading:a.value,bordered:!1,size:"small","row-key":y=>y.model,"max-height":560,"virtual-scroll":""},null,8,["data","loading","row-key"])]),_:1})]))}};export{Ee as default};
