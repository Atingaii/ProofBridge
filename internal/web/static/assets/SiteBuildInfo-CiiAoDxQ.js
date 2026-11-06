import{h as n,r as c,o as e}from"./index-DDgVjSJ6.js";/**
 * @license lucide-react v0.487.0 - ISC
 *
 * This source code is licensed under the ISC license.
 * See the LICENSE file in the root directory of this source tree.
 */const x=[["path",{d:"M5 12h14",key:"1ays0h"}],["path",{d:"m12 5 7 7-7 7",key:"xquz4c"}]],j=n("arrow-right",x);/**
 * @license lucide-react v0.487.0 - ISC
 *
 * This source code is licensed under the ISC license.
 * See the LICENSE file in the root directory of this source tree.
 */const b=[["circle",{cx:"12",cy:"12",r:"10",key:"1mglay"}],["path",{d:"m9 12 2 2 4-4",key:"dzmm74"}]],_=n("circle-check",b);/**
 * @license lucide-react v0.487.0 - ISC
 *
 * This source code is licensed under the ISC license.
 * See the LICENSE file in the root directory of this source tree.
 */const k=[["path",{d:"M15 3h6v6",key:"1q9fwt"}],["path",{d:"M10 14 21 3",key:"gplh6r"}],["path",{d:"M18 13v6a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2V8a2 2 0 0 1 2-2h6",key:"a6xqqp"}]],M=n("external-link",k);/**
 * @license lucide-react v0.487.0 - ISC
 *
 * This source code is licensed under the ISC license.
 * See the LICENSE file in the root directory of this source tree.
 */const f=[["path",{d:"M12 3a6 6 0 0 0 9 9 9 9 0 1 1-9-9Z",key:"a7tn18"}]],A=n("moon",f);/**
 * @license lucide-react v0.487.0 - ISC
 *
 * This source code is licensed under the ISC license.
 * See the LICENSE file in the root directory of this source tree.
 */const g=[["rect",{x:"16",y:"16",width:"6",height:"6",rx:"1",key:"4q2zg0"}],["rect",{x:"2",y:"16",width:"6",height:"6",rx:"1",key:"8cvhb9"}],["rect",{x:"9",y:"2",width:"6",height:"6",rx:"1",key:"1egb70"}],["path",{d:"M5 16v-3a1 1 0 0 1 1-1h12a1 1 0 0 1 1 1v3",key:"1jsf9p"}],["path",{d:"M12 12V8",key:"2874zd"}]],E=n("network",g);/**
 * @license lucide-react v0.487.0 - ISC
 *
 * This source code is licensed under the ISC license.
 * See the LICENSE file in the root directory of this source tree.
 */const v=[["circle",{cx:"12",cy:"12",r:"4",key:"4exip2"}],["path",{d:"M12 2v2",key:"tus03m"}],["path",{d:"M12 20v2",key:"1lh1kg"}],["path",{d:"m4.93 4.93 1.41 1.41",key:"149t6j"}],["path",{d:"m17.66 17.66 1.41 1.41",key:"ptbguv"}],["path",{d:"M2 12h2",key:"1t8f8n"}],["path",{d:"M20 12h2",key:"1q8mjw"}],["path",{d:"m6.34 17.66-1.41 1.41",key:"1m8zz5"}],["path",{d:"m19.07 4.93-1.41 1.41",key:"1shlcs"}]],z=n("sun",v);/**
 * @license lucide-react v0.487.0 - ISC
 *
 * This source code is licensed under the ISC license.
 * See the LICENSE file in the root directory of this source tree.
 */const w=[["path",{d:"M4 14a1 1 0 0 1-.78-1.63l9.9-10.2a.5.5 0 0 1 .86.46l-1.92 6.02A1 1 0 0 0 13 10h7a1 1 0 0 1 .78 1.63l-9.9 10.2a.5.5 0 0 1-.86-.46l1.92-6.02A1 1 0 0 0 11 14z",key:"1xq2db"}]],C=n("zap",w);var m={builtAt:"2026-11-06T15:13:37.967Z"};const i=typeof m>"u"?void 0:m;function T({language:u}){const[r,d]=c.useState({state:"loading"}),[p,y]=c.useState(0),t=(s,o)=>u==="zh"?s:o;return c.useEffect(()=>{const s=new AbortController;let o=!0;const l=window.setTimeout(()=>s.abort(),5e3);return d({state:"loading"}),(async()=>{try{const h=await fetch("/health",{signal:s.signal,cache:"no-store"});if(!h.ok)throw new Error("Hub unavailable");const a=await h.json();if(!a||typeof a!="object"||!("status"in a)||a.status!=="ok"||!("version"in a)||typeof a.version!="string"||!a.version.trim())throw new Error("Missing Hub version");o&&d({state:"ready",version:a.version.trim()})}catch{o&&d({state:"unavailable"})}finally{window.clearTimeout(l)}})(),()=>{o=!1,window.clearTimeout(l),s.abort()}},[p]),e.jsxs("section",{id:"build-info","aria-label":t("页面与服务版本","Page and service versions"),className:"mt-8 rounded-md border border-border bg-muted/25 p-4",children:[e.jsx("h2",{className:"text-sm font-semibold",children:t("页面与服务版本","Page and service versions")}),e.jsxs("dl",{className:"mt-3 grid gap-4 text-xs sm:grid-cols-2",children:[e.jsxs("div",{children:[e.jsx("dt",{className:"text-muted-foreground",children:t("前端构建时间（UTC）","Frontend build time (UTC)")}),e.jsx("dd",{className:"mt-1 break-words font-mono","data-testid":"frontend-build",children:i!=null&&i.builtAt?e.jsx("time",{dateTime:i.builtAt,children:i.builtAt}):t("构建信息不可用","Build metadata unavailable")}),e.jsxs("dd",{className:"mt-1 text-muted-foreground",children:[!1,t("标准界面","Standard interface")]})]}),e.jsxs("div",{children:[e.jsx("dt",{className:"text-muted-foreground",children:t("Hub 版本（当前站点 /health）","Hub version (this site’s /health)")}),e.jsx("dd",{className:"mt-1 break-words","aria-live":"polite","data-testid":"hub-version",children:r.state==="ready"?e.jsx("span",{className:"font-mono",children:r.version}):r.state==="loading"?t("正在查询…","Checking…"):e.jsxs(e.Fragment,{children:[t("暂时无法获取","Currently unavailable")," ",e.jsx("button",{type:"button",className:"ml-2 underline underline-offset-2 hover:text-primary",onClick:()=>y(s=>s+1),children:t("重试","Retry")})]})})]})]}),e.jsx("p",{className:"mt-3 text-xs leading-5 text-muted-foreground",children:t("前端构建、Hub 和各端点 Bridge 独立更新。历史发布记录不代表本站部署版本；端点版本以各自连接上报为准，本页不查询端点。","The frontend, Hub and each endpoint’s Bridge update independently. Release notes do not identify this site’s deployed build. Each endpoint reports its own version when connected; this page does not query endpoints.")})]})}export{j as A,_ as C,M as E,A as M,E as N,T as S,C as Z,z as a};
