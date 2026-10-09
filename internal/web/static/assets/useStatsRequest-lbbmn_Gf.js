import{h as o,r as s,d as k,S as m}from"./index-R2WV9RbB.js";/**
 * @license lucide-react v0.487.0 - ISC
 *
 * This source code is licensed under the ISC license.
 * See the LICENSE file in the root directory of this source tree.
 */const p=[["circle",{cx:"8",cy:"8",r:"6",key:"3yglwk"}],["path",{d:"M18.09 10.37A6 6 0 1 1 10.34 18",key:"t5s6rm"}],["path",{d:"M7 6h1v4",key:"1obek4"}],["path",{d:"m16.71 13.88.7.71-2.82 2.82",key:"1rbuyh"}]],M=o("coins",p);/**
 * @license lucide-react v0.487.0 - ISC
 *
 * This source code is licensed under the ISC license.
 * See the LICENSE file in the root directory of this source tree.
 */const g=[["path",{d:"M12.83 2.18a2 2 0 0 0-1.66 0L2.6 6.08a1 1 0 0 0 0 1.83l8.58 3.91a2 2 0 0 0 1.66 0l8.58-3.9a1 1 0 0 0 0-1.83z",key:"zw3jo"}],["path",{d:"M2 12a1 1 0 0 0 .58.91l8.6 3.91a2 2 0 0 0 1.65 0l8.58-3.9A1 1 0 0 0 22 12",key:"1wduqc"}],["path",{d:"M2 17a1 1 0 0 0 .58.91l8.6 3.91a2 2 0 0 0 1.65 0l8.58-3.9A1 1 0 0 0 22 17",key:"kqbvx6"}]],S=o("layers",g);/**
 * @license lucide-react v0.487.0 - ISC
 *
 * This source code is licensed under the ISC license.
 * See the LICENSE file in the root directory of this source tree.
 */const x=[["rect",{width:"20",height:"14",x:"2",y:"3",rx:"2",key:"48i651"}],["line",{x1:"8",x2:"16",y1:"21",y2:"21",key:"1svkeh"}],["line",{x1:"12",x2:"12",y1:"17",y2:"21",key:"vw1qmm"}]],q=o("monitor",x);function _(e,r,c,i){const[d,y]=s.useState(0),[l,n]=s.useState({key:e,data:null,loading:!0,error:""}),h=s.useCallback(()=>y(a=>a+1),[]);return s.useEffect(()=>{let a=!0;n({key:e,data:null,loading:!0,error:""});const u=k(r);return u.promise.then(t=>{a&&n({key:e,data:t,loading:!1,error:""})},t=>{const f=t instanceof m||t instanceof Error&&"status"in t&&t.status===504;a&&n({key:e,data:null,loading:!1,error:f?i:t instanceof Error?t.message:c})}),()=>{a=!1,u.cancel()}},[e,r,d,c,i]),{...l.key===e?l:{data:null,loading:!0,error:""},reload:h}}export{M as C,S as L,q as M,_ as u};
