import{T as G}from"./Select-DmDhZIkH.js";import{q as X,s as M,v as x,x as o,y as Y,z as V,D as U,E as ee,G as te,H as J,I as Q,J as oe,b as m,c as f,K as h,L as k,M as b,d as _,N as re,O as le,P as ne,Q as ae,R,S as se,T as H,u as ie,w as de,o as ce,j as D,f as S,g as $,F as pe,r as ue,e as be,t as me,C as q,U as ge,B as he,h as fe,l as ve,k as W,n as T,p as ye}from"./index-DGVFI0ve.js";import{D as xe}from"./DataTable-BvAjdHz0.js";var _e={thPaddingBorderedSmall:"8px 12px",thPaddingBorderedMedium:"12px 16px",thPaddingBorderedLarge:"16px 24px",thPaddingSmall:"0",thPaddingMedium:"0",thPaddingLarge:"0",tdPaddingBorderedSmall:"8px 12px",tdPaddingBorderedMedium:"12px 16px",tdPaddingBorderedLarge:"16px 24px",tdPaddingSmall:"0 0 8px 0",tdPaddingMedium:"0 0 12px 0",tdPaddingLarge:"0 0 16px 0"};function Ce(l){const{tableHeaderColor:g,textColor2:s,textColor1:a,cardColor:n,modalColor:i,popoverColor:v,dividerColor:c,borderRadius:p,fontWeightStrong:y,lineHeight:C,fontSizeSmall:r,fontSizeMedium:w,fontSizeLarge:z}=l;return{..._e,lineHeight:C,fontSizeSmall:r,fontSizeMedium:w,fontSizeLarge:z,titleTextColor:a,thColor:M(n,g),thColorModal:M(i,g),thColorPopover:M(v,g),thTextColor:a,thFontWeight:y,tdTextColor:s,tdColor:n,tdColorModal:i,tdColorPopover:v,borderColor:M(n,c),borderColorModal:M(i,c),borderColorPopover:M(v,c),borderRadius:p}}const we={common:X,self:Ce};function K(l,g="default",s=[]){const{children:a}=l;if(a!==null&&typeof a=="object"&&!Array.isArray(a)){const n=a[g];if(typeof n=="function")return n()}return s}var Se=x([o("descriptions",{fontSize:"var(--n-font-size)"},[o("descriptions-separator",`
 display: inline-block;
 margin: 0 8px 0 2px;
 `),o("descriptions-table-wrapper",[o("descriptions-table",[o("descriptions-table-row",[o("descriptions-table-header",{padding:"var(--n-th-padding)"}),o("descriptions-table-content",{padding:"var(--n-td-padding)"})])])]),Y("bordered",[o("descriptions-table-wrapper",[o("descriptions-table",[o("descriptions-table-row",[x("&:last-child",[o("descriptions-table-content",{paddingBottom:0})])])])])]),V("left-label-placement",[o("descriptions-table-content",[x("> *",{verticalAlign:"top"})])]),V("left-label-align",[x("th",{textAlign:"left"})]),V("center-label-align",[x("th",{textAlign:"center"})]),V("right-label-align",[x("th",{textAlign:"right"})]),V("bordered",[o("descriptions-table-wrapper",`
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
 `))]);const ze="DESCRIPTION_ITEM_FLAG";function Pe(l){return typeof l=="object"&&l&&!Array.isArray(l)?l.type&&l.type.DESCRIPTION_ITEM_FLAG:!1}const ke=["colspan"],$e=["colspan"],Te=["colspan"],Re=["colspan"],Be={...Q.props,title:String,column:{type:Number,default:3},columns:Number,labelPlacement:{type:String,default:"top"},labelAlign:{type:String,default:"left"},separator:{type:String,default:":"},size:String,bordered:Boolean,labelClass:String,labelStyle:[Object,String],contentClass:String,contentStyle:[Object,String]};var Le=J({name:"Descriptions",props:Be,slots:Object,setup(l){const{mergedClsPrefixRef:g,inlineThemeDisabled:s,mergedComponentPropsRef:a}=ne(l),n=R(()=>l.size||a?.value?.Descriptions?.size||"medium"),i=Q("Descriptions","-descriptions",Se,we,l,g),v=R(()=>{const{bordered:p}=l,y=n.value,{common:{cubicBezierEaseInOut:C},self:{titleTextColor:r,thColor:w,thColorModal:z,thColorPopover:P,thTextColor:I,thFontWeight:e,tdTextColor:t,tdColor:u,tdColorModal:d,tdColorPopover:B,borderColor:A,borderColorModal:N,borderColorPopover:L,borderRadius:O,lineHeight:j,[H("fontSize",y)]:E,[H(p?"thPaddingBordered":"thPadding",y)]:F,[H(p?"tdPaddingBordered":"tdPadding",y)]:Z}}=i.value;return{"--n-title-text-color":r,"--n-th-padding":F,"--n-td-padding":Z,"--n-font-size":E,"--n-bezier":C,"--n-th-font-weight":e,"--n-line-height":j,"--n-th-text-color":I,"--n-td-text-color":t,"--n-th-color":w,"--n-th-color-modal":z,"--n-th-color-popover":P,"--n-td-color":u,"--n-td-color-modal":d,"--n-td-color-popover":B,"--n-border-radius":O,"--n-border-color":A,"--n-border-color-modal":N,"--n-border-color-popover":L}}),c=s?ae("descriptions",R(()=>{let p="";const{bordered:y}=l;return y&&(p+="a"),p+=n.value[0],p}),v,l):void 0;return{mergedClsPrefix:g,cssVars:s?void 0:v,themeClass:c?.themeClass,onRender:c?.onRender,compitableColumn:se(l,["columns","column"]),inlineThemeDisabled:s,mergedSize:n}},render(){const l=this.$slots.default,g=l?oe(l()):[];g.length;const{contentClass:s,labelClass:a,compitableColumn:n,labelPlacement:i,labelAlign:v,mergedSize:c,bordered:p,title:y,cssVars:C,mergedClsPrefix:r,separator:w,onRender:z}=this;z?.();const P=g.filter(e=>Pe(e)),I=P.reduce((e,t,u)=>{const d=t.props||{},B=P.length-1===u,A=["label"in d?d.label:K(t,"label")],N=[K(t)],L=d.span||1,O=e.span;e.span+=L;const j=d.labelStyle||d["label-style"]||this.labelStyle,E=d.contentStyle||d["content-style"]||this.contentStyle;if(i==="left")p?e.row.push((m(),f("th",{key:1,class:b([`${r}-descriptions-table-header`,a]),colspan:1,style:k(j)},[h(()=>A)],6)),(m(),f("td",{key:2,class:b([`${r}-descriptions-table-content`,s]),colspan:B?(n-O)*2+1:L*2-1,style:k(E)},[h(()=>N)],14,ke))):e.row.push((m(),f("td",{key:3,class:b(`${r}-descriptions-table-content`),colspan:B?(n-O)*2:L*2},[_("span",{class:b([`${r}-descriptions-table-content__label`,a]),style:k(j)},[h(()=>[...A,w&&(m(),f("span",{key:4,class:b(`${r}-descriptions-separator`)},[h(()=>w)],2))])],6),_("span",{class:b([`${r}-descriptions-table-content__content`,s]),style:k(E)},[h(()=>N)],6)],10,$e)));else{const F=B?(n-O)*2:L*2;e.row.push((m(),f("th",{key:5,class:b([`${r}-descriptions-table-header`,a]),colspan:F,style:k(j)},[h(()=>A)],14,Te))),e.secondRow.push((m(),f("td",{key:6,class:b([`${r}-descriptions-table-content`,s]),colspan:F,style:k(E)},[h(()=>N)],14,Re)))}return(e.span>=n||B)&&(e.span=0,e.row.length&&(e.rows.push(e.row),e.row=[]),i!=="left"&&e.secondRow.length&&(e.rows.push(e.secondRow),e.secondRow=[])),e},{span:0,row:[],secondRow:[],rows:[]}).rows.map(e=>(m(),f("tr",{class:b(`${r}-descriptions-table-row`)},[h(()=>e)],2)));return m(),f("div",{style:k(C),class:b([`${r}-descriptions`,this.themeClass,`${r}-descriptions--${i}-label-placement`,`${r}-descriptions--${v}-label-align`,`${r}-descriptions--${c}-size`,p&&`${r}-descriptions--bordered`])},[y||this.$slots.header?(m(),f("div",{key:0,class:b(`${r}-descriptions-header`)},[h(()=>y||re(this,"header"))],2)):h(()=>null),_("div",{class:b(`${r}-descriptions-table-wrapper`)},[_("table",{class:b(`${r}-descriptions-table`)},[_("tbody",null,[h(()=>i==="top"&&(m(),f("tr",{class:b(`${r}-descriptions-table-row`),style:{visibility:"collapse"}},[h(()=>le(n*2,(m(),f("td"))))],2))),h(()=>I)])],2)],2)],6)}});const Me={label:String,span:{type:Number,default:1},labelClass:String,labelStyle:[Object,String],contentClass:String,contentStyle:[Object,String]};var De=J({name:"DescriptionsItem",[ze]:!0,props:Me,slots:Object,render(){return null}});const Ie={class:"mono"},Ae={class:"card-toolbar"},Ee={__name:"DiagnosticsView",props:{reloadSignal:{type:Number,default:0}},setup(l){const g=l,s=ie(),a=W(!1),n=W(!1),i=W(null),v=W(""),c=R(()=>i.value?.metadata||{}),p=R(()=>i.value?.catalog||{}),y=R(()=>[["元数据就绪",c.value.ready?"是":"否"],["价格模型数",Number(c.value.models||0).toLocaleString()],["更新时间",C(c.value.updated_at)],["是否过期",c.value.stale?"是":"否"],["下次刷新",C(c.value.next_refresh)],["最近错误",c.value.last_error||"无"],["上游模型",`Zen ${p.value.zen||0} · Go ${p.value.go||0}`],["可暴露模型",Number(p.value.exposed||0).toLocaleString()]]);function C(e){if(!e)return"—";const t=new Date(e);return Number.isNaN(t.getTime())?"—":t.toLocaleString()}const r=R(()=>{const e=v.value.trim().toLowerCase();return(i.value?.models||[]).filter(t=>!e||t.model.toLowerCase().includes(e)||(t.alias||"").toLowerCase().includes(e))}),w={name_free:"名称含 free",name_and_metadata_free:"名称与价格均为免费",metadata_free:"价格为零",metadata_paid:"价格为付费",metadata_deprecated:"已弃用",metadata_cost_unknown:"价格未知",metadata_model_missing:"元数据未收录",metadata_pending:"元数据未就绪",name_fallback_metadata_pending:"名称推断（元数据未就绪）"},z=[{title:"模型",key:"model",minWidth:230,render:e=>T("div",null,[T("div",{class:"mono"},e.model),e.alias?T("div",{class:"section-caption"},`对外 ${e.alias}`):null])},{title:"原生协议",key:"protocol",width:170,render:e=>`${e.native_protocol||"—"} · ${e.protocol_source||"—"}`},{title:"路由",key:"route",minWidth:150,render:e=>{if(e.route_error)return T(G,{type:"error",size:"small",bordered:!1,title:e.route_error},{default:()=>"不可路由"});const t=[...e.anonymous?["anonymous"]:[],...e.key_tiers||[]];return t.length?t.join(" → "):"—"}},{title:"匿名资格",key:"anonymous",width:100,render:e=>{const t=e.anonymous_eligibility?.allowed;return T(G,{type:t?"success":"default",size:"small",bordered:!1},{default:()=>t?"允许":"拒绝"})}},{title:"判断来源",key:"source",minWidth:200,ellipsis:{tooltip:!0},render:e=>{const t=e.anonymous_eligibility?.source,u=e.anonymous_eligibility?.deprecated,d=t?w[t]||t:"—";return u?T("div",{class:"row-with-tag"},[d,T(G,{type:"warning",size:"small",bordered:!1},{default:()=>"已弃用"})]):d}},{title:"成本 input / output",key:"cost",minWidth:170,render:e=>{const t=e.anonymous_eligibility||{};if(t.input_cost==null&&t.output_cost==null)return"未知";const u=t.input_cost==null?"?":t.input_cost,d=t.output_cost==null?"?":t.output_cost;return`${u} / ${d}`}}];async function P(e=!1){a.value=!0;try{i.value=await ve("/api/debug/models"),e&&s.success("诊断数据已刷新")}catch(t){e&&s.error(t.message)}finally{a.value=!1}}async function I(){n.value=!0;try{i.value=await ye("/api/models/refresh",{}),s.success("已从上游重新拉取模型目录与价格元数据")}catch(e){s.error(e.message)}finally{n.value=!1}}return de(()=>g.reloadSignal,()=>P(!0)),ce(()=>P(!1)),(e,t)=>(m(),f("div",null,[D($(q),{title:"模型元数据",size:"small",bordered:!1,class:"block-gap"},{"header-extra":S(()=>[...t[1]||(t[1]=[_("span",{class:"section-caption"}," 零成本或名称含 free 任一条件满足即可进入匿名通道 ",-1)])]),default:S(()=>[D($(Le),{column:4,"label-placement":"top",size:"small"},{default:S(()=>[(m(!0),f(pe,null,ue(y.value,([u,d])=>(m(),be($(De),{key:u,label:u},{default:S(()=>[_("span",Ie,me(d),1)]),_:2},1032,["label"]))),128))]),_:1})]),_:1}),D($(q),{size:"small",bordered:!1},{header:S(()=>[...t[2]||(t[2]=[_("span",null,"模型路由表",-1)])]),"header-extra":S(()=>[_("div",Ae,[D($(ge),{value:v.value,"onUpdate:value":t[0]||(t[0]=u=>v.value=u),placeholder:"筛选模型",clearable:"",size:"small",style:{width:"220px"}},null,8,["value"]),D($(he),{size:"small",loading:n.value,onClick:I},{default:S(()=>[...t[3]||(t[3]=[fe("刷新",-1)])]),_:1},8,["loading"])])]),default:S(()=>[D($(xe),{columns:z,data:r.value,loading:a.value,bordered:!1,size:"small","row-key":u=>u.model,"max-height":560,"virtual-scroll":""},null,8,["data","loading","row-key"])]),_:1})]))}};export{Ee as default};
