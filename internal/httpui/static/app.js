var htmx=function(){"use strict";const Q={onLoad:null,process:null,on:null,off:null,trigger:null,ajax:null,find:null,findAll:null,closest:null,values:function(e,t){const n=dn(e,t||"post");return n.values},remove:null,addClass:null,removeClass:null,toggleClass:null,takeClass:null,swap:null,defineExtension:null,removeExtension:null,logAll:null,logNone:null,logger:null,config:{historyEnabled:true,historyCacheSize:10,refreshOnHistoryMiss:false,defaultSwapStyle:"innerHTML",defaultSwapDelay:0,defaultSettleDelay:20,includeIndicatorStyles:true,indicatorClass:"htmx-indicator",requestClass:"htmx-request",addedClass:"htmx-added",settlingClass:"htmx-settling",swappingClass:"htmx-swapping",allowEval:true,allowScriptTags:true,inlineScriptNonce:"",inlineStyleNonce:"",attributesToSettle:["class","style","width","height"],withCredentials:false,timeout:0,wsReconnectDelay:"full-jitter",wsBinaryType:"blob",disableSelector:"[hx-disable], [data-hx-disable]",scrollBehavior:"instant",defaultFocusScroll:false,getCacheBusterParam:false,globalViewTransitions:false,methodsThatUseUrlParams:["get","delete"],selfRequestsOnly:true,ignoreTitle:false,scrollIntoViewOnBoost:true,triggerSpecsCache:null,disableInheritance:false,responseHandling:[{code:"204",swap:false},{code:"[23]..",swap:true},{code:"[45]..",swap:false,error:true}],allowNestedOobSwaps:true,historyRestoreAsHxRequest:true,reportValidityOfForms:false},parseInterval:null,location:location,_:null,version:"2.0.10"};Q.onLoad=j;Q.process=Ft;Q.on=ye;Q.off=xe;Q.trigger=ae;Q.ajax=Nn;Q.find=f;Q.findAll=y;Q.closest=g;Q.remove=z;Q.addClass=w;Q.removeClass=b;Q.toggleClass=G;Q.takeClass=W;Q.swap=_e;Q.defineExtension=_n;Q.removeExtension=zn;Q.logAll=$;Q.logNone=_;Q.parseInterval=d;Q._=e;const n={addTriggerHandler:St,bodyContains:se,canAccessLocalStorage:U,findThisElement:we,filterValues:yn,swap:_e,hasAttribute:s,getAttributeValue:a,getClosestAttributeValue:ne,getClosestMatch:A,getExpressionVars:Rn,getHeaders:mn,getInputValues:dn,getInternalData:oe,getSwapSpecification:bn,getTriggerSpecs:st,getTarget:Se,makeFragment:P,mergeObjects:le,makeSettleInfo:Sn,oobSwap:He,querySelectorExt:ce,settleImmediately:Yt,shouldCancel:ht,triggerEvent:ae,triggerErrorEvent:fe,withExtensions:Vt};const de=["get","post","put","delete","patch"];const R=de.map(function(e){return"[hx-"+e+"], [data-hx-"+e+"]"}).join(", ");function d(e){if(e==undefined){return undefined}let t=NaN;if(e.slice(-2)=="ms"){t=parseFloat(e.slice(0,-2))}else if(e.slice(-1)=="s"){t=parseFloat(e.slice(0,-1))*1e3}else if(e.slice(-1)=="m"){t=parseFloat(e.slice(0,-1))*1e3*60}else{t=parseFloat(e)}return isNaN(t)?undefined:t}function ee(e,t){return e instanceof Element&&e.getAttribute(t)}function s(e,t){return!!e.hasAttribute&&(e.hasAttribute(t)||e.hasAttribute("data-"+t))}function a(e,t){return ee(e,t)||ee(e,"data-"+t)}function c(e){const t=e.parentElement;if(!t&&e.parentNode instanceof ShadowRoot)return e.parentNode;return t}function te(){return document}function q(e,t){return e.getRootNode?e.getRootNode({composed:t}):te()}function A(e,t){while(e&&!t(e)){e=c(e)}return e||null}function o(e,t,n){const r=a(t,n);const o=a(t,"hx-disinherit");var i=a(t,"hx-inherit");if(e!==t){if(Q.config.disableInheritance){if(i&&(i==="*"||i.split(" ").indexOf(n)>=0)){return r}else{return null}}if(o&&(o==="*"||o.split(" ").indexOf(n)>=0)){return"unset"}}return r}function ne(t,n){let r=null;A(t,function(e){return!!(r=o(t,ue(e),n))});if(r!=="unset"){return r}}function h(e,t){return e instanceof Element&&e.matches(t)}function N(e){const t=/<([a-z][^\/\0>\x20\t\r\n\f]*)/i;const n=t.exec(e);if(n){return n[1].toLowerCase()}else{return""}}function I(e){if("parseHTMLUnsafe"in Document){return Document.parseHTMLUnsafe(e)}const t=new DOMParser;return t.parseFromString(e,"text/html")}function L(e,t){while(t.childNodes.length>0){e.append(t.childNodes[0])}}function r(e){const t=te().createElement("script");ie(e.attributes,function(e){t.setAttribute(e.name,e.value)});t.textContent=e.textContent;t.async=false;if(Q.config.inlineScriptNonce){t.nonce=Q.config.inlineScriptNonce}return t}function i(e){return e.matches("script")&&(e.type==="text/javascript"||e.type==="module"||e.type==="")}function D(e){Array.from(e.querySelectorAll("script")).forEach(e=>{if(i(e)){const t=r(e);const n=e.parentNode;try{n.insertBefore(t,e)}catch(e){H(e)}finally{e.remove()}}})}function P(e){const t=e.replace(/<head(\s[^>]*)?>[\s\S]*?<\/head>/i,"");const n=N(t);let r;if(n==="html"){r=new DocumentFragment;const i=I(e);L(r,i.body);r.title=i.title}else if(n==="body"){r=new DocumentFragment;const i=I(t);L(r,i.body);r.title=i.title}else{const i=I('<body><template class="internal-htmx-wrapper">'+t+"</template></body>");r=i.querySelector("template").content;r.title=i.title;var o=r.querySelector("title");if(o&&o.parentNode===r){o.remove();r.title=o.innerText}}if(r){if(Q.config.allowScriptTags){D(r)}else{r.querySelectorAll("script").forEach(e=>e.remove())}}return r}function re(e){if(e){e()}}function t(e,t){return Object.prototype.toString.call(e)==="[object "+t+"]"}function k(e){return typeof e==="function"}function M(e){return t(e,"Object")}function oe(e){const t="htmx-internal-data";let n=e[t];if(!n){n=e[t]={}}return n}function F(t){const n=[];if(t){for(let e=0;e<t.length;e++){n.push(t[e])}}return n}function ie(t,n){if(t){for(let e=0;e<t.length;e++){n(t[e])}}}function B(e){const t=e.getBoundingClientRect();const n=t.top;const r=t.bottom;return n<window.innerHeight&&r>=0}function se(e){return e.getRootNode({composed:true})===document}function X(e){return e.trim().split(/\s+/)}function le(e,t){for(const n in t){if(t.hasOwnProperty(n)){e[n]=t[n]}}return e}function v(e){try{return JSON.parse(e)}catch(e){H(e);return null}}function U(){const e="htmx:sessionStorageTest";try{sessionStorage.setItem(e,e);sessionStorage.removeItem(e);return true}catch(e){return false}}function V(e){try{const t=new URL(e,window.location.href);e=t.pathname+t.search}catch(e){}if(e!="/"){e=e.replace(/\/+$/,"")}return e}function e(e){return On(te().body,function(){return eval(e)})}function j(t){const e=Q.on("htmx:load",function(e){t(e.detail.elt)});return e}function $(){Q.logger=function(e,t,n){if(console){console.log(t,e,n)}}}function _(){Q.logger=null}function f(e,t){if(typeof e!=="string"){return e.querySelector(t)}else{return f(te(),e)}}function y(e,t){if(typeof e!=="string"){return e.querySelectorAll(t)}else{return y(te(),e)}}function x(){return window}function z(e,t){e=S(e);if(t){x().setTimeout(function(){z(e);e=null},t)}else{c(e).removeChild(e)}}function ue(e){return e instanceof Element?e:null}function J(e){return e instanceof HTMLElement?e:null}function K(e){return typeof e==="string"?e:null}function p(e){return e instanceof Element||e instanceof Document||e instanceof DocumentFragment?e:null}function w(e,t,n){e=ue(S(e));if(!e){return}if(n){x().setTimeout(function(){w(e,t);e=null},n)}else{e.classList&&e.classList.add(t)}}function b(e,t,n){let r=ue(S(e));if(!r){return}if(n){x().setTimeout(function(){b(r,t);r=null},n)}else{if(r.classList){r.classList.remove(t);if(r.classList.length===0){r.removeAttribute("class")}}}}function G(e,t){e=S(e);e.classList.toggle(t)}function W(e,t){e=S(e);ie(e.parentElement.children,function(e){b(e,t)});w(ue(e),t)}function g(e,t){e=ue(S(e));if(e){return e.closest(t)}return null}function l(e,t){return e.substring(0,t.length)===t}function Z(e,t){return e.substring(e.length-t.length)===t}function Y(e){const t=e.trim();if(l(t,"<")&&Z(t,"/>")){return t.substring(1,t.length-2)}else{return t}}function m(t,r,n){if(r.indexOf("global ")===0){return m(t,r.slice(7),true)}t=S(t);const o=[];{let t=0;let n=0;for(let e=0;e<r.length;e++){const l=r[e];if(l===","&&t===0){o.push(r.substring(n,e));n=e+1;continue}if(l==="<"){t++}else if(l==="/"&&e<r.length-1&&r[e+1]===">"){t--}}if(n<r.length){o.push(r.substring(n))}}const i=[];const s=[];while(o.length>0){const r=Y(o.shift());let e;if(r.indexOf("closest ")===0){e=g(ue(t),Y(r.slice(8)))}else if(r.indexOf("find ")===0){e=f(p(t),Y(r.slice(5)))}else if(r==="next"||r==="nextElementSibling"){e=ue(t).nextElementSibling}else if(r.indexOf("next ")===0){e=pe(t,Y(r.slice(5)),!!n)}else if(r==="previous"||r==="previousElementSibling"){e=ue(t).previousElementSibling}else if(r.indexOf("previous ")===0){e=ge(t,Y(r.slice(9)),!!n)}else if(r==="document"){e=document}else if(r==="window"){e=window}else if(r==="body"){e=document.body}else if(r==="root"){e=q(t,!!n)}else if(r==="host"){e=t.getRootNode().host}else{s.push(r)}if(e){i.push(e)}}if(s.length>0){const e=s.join(",");const u=p(q(t,!!n));i.push(...F(u.querySelectorAll(e)))}return i}var pe=function(t,e,n){const r=p(q(t,n)).querySelectorAll(e);for(let e=0;e<r.length;e++){const o=r[e];if(o.compareDocumentPosition(t)===Node.DOCUMENT_POSITION_PRECEDING){return o}}};var ge=function(t,e,n){const r=p(q(t,n)).querySelectorAll(e);for(let e=r.length-1;e>=0;e--){const o=r[e];if(o.compareDocumentPosition(t)===Node.DOCUMENT_POSITION_FOLLOWING){return o}}};function ce(e,t){if(typeof e!=="string"){return m(e,t)[0]}else{return m(te().body,e)[0]}}function S(e,t){if(typeof e==="string"){return f(p(t)||document,e)}else{return e}}function me(e,t,n,r){if(k(t)){return{target:te().body,event:K(e),listener:t,options:n}}else{return{target:S(e),event:K(t),listener:n,options:r}}}function ye(t,n,r,o){Gn(function(){const e=me(t,n,r,o);e.target.addEventListener(e.event,e.listener,e.options)});const e=k(n);return e?n:r}function xe(t,n,r){Gn(function(){const e=me(t,n,r);e.target.removeEventListener(e.event,e.listener)});return k(n)?n:r}const be=te().createElement("output");function ve(t,n){const e=ne(t,n);if(e){if(e==="this"){return[we(t,n)]}else{const r=m(t,e);const o=/(^|,)(\s*)inherit(\s*)($|,)/.test(e);if(o){const i=ue(A(t,function(e){return e!==t&&s(ue(e),n)}));if(i){r.push(...ve(i,n))}}if(r.length===0){H('The selector "'+e+'" on '+n+" returned no matches!");return[be]}else{return r}}}}function we(e,t){return ue(A(e,function(e){return a(ue(e),t)!=null}))}function Se(e){const t=ne(e,"hx-target");if(t){if(t==="this"){return we(e,"hx-target")}else{return ce(e,t)}}else{const n=oe(e);if(n.boosted){return te().body}else{return e}}}function Ee(e){return Q.config.attributesToSettle.includes(e)}function Ce(t,n){ie(Array.from(t.attributes),function(e){if(!n.hasAttribute(e.name)&&Ee(e.name)){t.removeAttribute(e.name)}});ie(n.attributes,function(e){if(Ee(e.name)){t.setAttribute(e.name,e.value)}})}function Oe(t,e){const n=Jn(e);for(let e=0;e<n.length;e++){const r=n[e];try{if(r.isInlineSwap(t)){return true}}catch(e){H(e)}}return t==="outerHTML"}function He(e,o,i,t){t=t||te();let n="#"+CSS.escape(ee(o,"id"));let s="outerHTML";if(e==="true"){}else if(e.indexOf(":")>0){s=e.substring(0,e.indexOf(":"));n=e.substring(e.indexOf(":")+1)}else{s=e}o.removeAttribute("hx-swap-oob");o.removeAttribute("data-hx-swap-oob");const r=m(t,n,false);if(r.length){ie(r,function(e){let t;const n=o.cloneNode(true);t=te().createDocumentFragment();t.appendChild(n);if(!Oe(s,e)){t=p(n)}const r={shouldSwap:true,target:e,fragment:t};if(!ae(e,"htmx:oobBeforeSwap",r))return;e=r.target;if(r.shouldSwap){Re(t);je(s,e,e,t,i);Te()}ie(i.elts,function(e){ae(e,"htmx:oobAfterSwap",r)})});o.parentNode.removeChild(o)}else{o.parentNode.removeChild(o);fe(te().body,"htmx:oobErrorNoTarget",{content:o,target:n})}return e}function Te(){const e=f("#--htmx-preserve-pantry--");if(e){for(const t of[...e.children]){const n=f("#"+t.id);n.parentNode.moveBefore(t,n);n.remove()}e.remove()}}function Re(e){ie(y(e,"[hx-preserve], [data-hx-preserve]"),function(e){const t=a(e,"id");const n=te().getElementById(t);if(n!=null){if(e.moveBefore){let e=f("#--htmx-preserve-pantry--");if(e==null){te().body.insertAdjacentHTML("afterend","<div id='--htmx-preserve-pantry--'></div>");e=f("#--htmx-preserve-pantry--")}e.moveBefore(n,null)}else{e.parentNode.replaceChild(n,e)}}})}function qe(i,e,s){ie(e.querySelectorAll("[id]"),function(t){const n=ee(t,"id");if(n&&n.length>0){const e=p(i);const r=e&&e.querySelector(CSS.escape(t.tagName)+"#"+CSS.escape(n));if(r&&r!==e){const o=t.cloneNode();Ce(t,r);s.tasks.push(function(){Ce(t,o)})}}})}function Ae(e){return function(){b(e,Q.config.addedClass);Ft(ue(e));Ne(p(e));ae(e,"htmx:load")}}function Ne(e){const t="[autofocus]";const n=J(h(e,t)?e:e.querySelector(t));if(n!=null){n.focus()}}function u(e,t,n,r){qe(e,n,r);while(n.childNodes.length>0){const o=n.firstChild;w(ue(o),Q.config.addedClass);e.insertBefore(o,t);if(o.nodeType!==Node.TEXT_NODE&&o.nodeType!==Node.COMMENT_NODE){r.tasks.push(Ae(o))}}}function Ie(e,t){let n=0;while(n<e.length){t=(t<<5)-t+e.charCodeAt(n++)|0}return t}function Le(t){let n=0;for(let e=0;e<t.attributes.length;e++){const r=t.attributes[e];if(r.value){n=Ie(r.name,n);n=Ie(r.value,n)}}return n}function De(t){const n=oe(t);if(n.onHandlers){for(let e=0;e<n.onHandlers.length;e++){const r=n.onHandlers[e];xe(t,r.event,r.listener)}delete n.onHandlers}}function Pe(e){const t=oe(e);if(t.timeout){clearTimeout(t.timeout)}if(t.listenerInfos){ie(t.listenerInfos,function(e){if(e.on){xe(e.on,e.trigger,e.listener)}})}De(e);ie(Object.keys(t),function(e){if(e!=="firstInitCompleted")delete t[e]})}function E(e){ae(e,"htmx:beforeCleanupElement");Pe(e);ie(e.children,function(e){E(e)})}function ke(t,e,n){if(t.tagName==="BODY"){return Ve(t,e,n)}let r;const o=t.previousSibling;const i=c(t);if(!i){return}u(i,t,e,n);if(o==null){r=i.firstChild}else{r=o.nextSibling}n.elts=n.elts.filter(function(e){return e!==t});while(r&&r!==t){if(r instanceof Element){n.elts.push(r)}r=r.nextSibling}E(t);t.remove()}function Me(e,t,n){return u(e,e.firstChild,t,n)}function Fe(e,t,n){return u(c(e),e,t,n)}function Be(e,t,n){return u(e,null,t,n)}function Xe(e,t,n){return u(c(e),e.nextSibling,t,n)}function Ue(e){E(e);const t=c(e);if(t){return t.removeChild(e)}}function Ve(e,t,n){const r=e.firstChild;u(e,r,t,n);if(r){while(r.nextSibling){E(r.nextSibling);e.removeChild(r.nextSibling)}E(r);e.removeChild(r)}}function je(t,e,n,r,o){switch(t){case"none":return;case"outerHTML":ke(n,r,o);return;case"afterbegin":Me(n,r,o);return;case"beforebegin":Fe(n,r,o);return;case"beforeend":Be(n,r,o);return;case"afterend":Xe(n,r,o);return;case"delete":Ue(n);return;default:var i=Jn(e);for(let e=0;e<i.length;e++){const s=i[e];try{const l=s.handleSwap(t,n,r,o);if(l){if(Array.isArray(l)){for(let e=0;e<l.length;e++){const u=l[e];if(u.nodeType!==Node.TEXT_NODE&&u.nodeType!==Node.COMMENT_NODE){o.tasks.push(Ae(u))}}}return}}catch(e){H(e)}}if(t==="innerHTML"){Ve(n,r,o)}else{je(Q.config.defaultSwapStyle,e,n,r,o)}}}function $e(e,n,r){var t=y(e,"[hx-swap-oob], [data-hx-swap-oob]");ie(t,function(e){if(Q.config.allowNestedOobSwaps||e.parentElement===null){const t=a(e,"hx-swap-oob");if(t!=null){He(t,e,n,r)}}else{e.removeAttribute("hx-swap-oob");e.removeAttribute("data-hx-swap-oob")}});return t.length>0}function _e(h,d,p,g){if(!g){g={}}let m=null;let n=null;let e=function(){re(g.beforeSwapCallback);h=S(h);const r=g.contextElement?q(g.contextElement,false):te();const e=document.activeElement;let t={};t={elt:e,start:e?e.selectionStart:null,end:e?e.selectionEnd:null};const o=Sn(h);if(p.swapStyle==="textContent"){h.textContent=d}else{let n=P(d);o.title=g.title||n.title;if(g.historyRequest){n=n.querySelector("[hx-history-elt],[data-hx-history-elt]")||n}if(g.selectOOB){const i=g.selectOOB.split(",");for(let t=0;t<i.length;t++){const s=i[t].split(":",2);let e=s[0].trim();if(e.indexOf("#")===0){e=e.substring(1)}const l=s[1]||"true";const u=n.querySelector("#"+e);if(u){He(l,u,o,r)}}}$e(n,o,r);ie(y(n,"template"),function(e){if(e.content&&$e(e.content,o,r)){e.remove()}});if(g.select){const c=te().createDocumentFragment();ie(n.querySelectorAll(g.select),function(e){c.appendChild(e)});n=c}Re(n);je(p.swapStyle,g.contextElement,h,n,o);Te()}if(t.elt&&!se(t.elt)&&ee(t.elt,"id")){const f=document.getElementById(ee(t.elt,"id"));const a={preventScroll:p.focusScroll!==undefined?!p.focusScroll:!Q.config.defaultFocusScroll};if(f){if(t.start&&f.setSelectionRange){try{f.setSelectionRange(t.start,t.end)}catch(e){}}f.focus(a)}}b(h,Q.config.swappingClass);ie(o.elts,function(e){if(e.classList){w(e,Q.config.settlingClass)}ae(e,"htmx:afterSwap",g.eventInfo)});re(g.afterSwapCallback);if(!p.ignoreTitle){Xn(o.title)}const n=function(){ie(o.tasks,function(e){e.call()});ie(o.elts,function(e){if(e.classList){b(e,Q.config.settlingClass)}ae(e,"htmx:afterSettle",g.eventInfo)});if(g.anchor){const e=ue(S("#"+g.anchor));if(e){e.scrollIntoView({block:"start",behavior:"auto"})}}En(o.elts,p);re(g.afterSettleCallback);re(m)};if(p.settleDelay>0){x().setTimeout(n,p.settleDelay)}else{n()}};let t=Q.config.globalViewTransitions;if(p.hasOwnProperty("transition")){t=p.transition}const r=g.contextElement||te();if(t&&ae(r,"htmx:beforeTransition",g.eventInfo)&&typeof Promise!=="undefined"&&document.startViewTransition){const o=new Promise(function(e,t){m=e;n=t});const i=e;e=function(){document.startViewTransition(function(){i();return o})}}try{if(p?.swapDelay&&p.swapDelay>0){x().setTimeout(e,p.swapDelay)}else{e()}}catch(e){fe(r,"htmx:swapError",g.eventInfo);re(n);throw e}}function ze(e,t,n){const r=e.getResponseHeader(t);if(r.indexOf("{")===0){const o=v(r);for(const i in o){if(o.hasOwnProperty(i)){let e=o[i];if(M(e)){n=e.target!==undefined?e.target:n}else{e={value:e}}ae(n,i,e)}}}else{const s=r.split(",");for(let e=0;e<s.length;e++){ae(n,s[e].trim(),[])}}}const Je=/\s/;const C=/[\s,]/;const Ke=/[_$a-zA-Z]/;const Ge=/[_$a-zA-Z0-9]/;const We=['"',"'","/"];const Ze=/[^\s]/;const Ye=/[{(]/;const Qe=/[})]/;function et(e){const t=[];let n=0;while(n<e.length){if(Ke.exec(e.charAt(n))){var r=n;while(Ge.exec(e.charAt(n+1))){n++}t.push(e.substring(r,n+1))}else if(We.indexOf(e.charAt(n))!==-1){const o=e.charAt(n);var r=n;n++;while(n<e.length&&e.charAt(n)!==o){if(e.charAt(n)==="\\"){n++}n++}t.push(e.substring(r,n+1))}else{const i=e.charAt(n);t.push(i)}n++}return t}function tt(e,t,n){return Ke.exec(e.charAt(0))&&e!=="true"&&e!=="false"&&e!=="this"&&e!==n&&t!=="."}function nt(r,o,i){if(o[0]==="["){o.shift();let e=1;let t=" return (function("+i+"){ return (";let n=null;while(o.length>0){const s=o[0];if(s==="]"){e--;if(e===0){if(n===null){t=t+"true"}o.shift();t+=")})";try{const l=On(r,function(){return Function(t)()},function(){return true});l.source=t;return l}catch(e){fe(te().body,"htmx:syntax:error",{error:e,source:t});return null}}}else if(s==="["){e++}if(tt(s,n,i)){t+="(("+i+"."+s+") ? ("+i+"."+s+") : (window."+s+"))"}else{t=t+s}n=o.shift()}}}function O(e,t){let n="";while(e.length>0&&!t.test(e[0])){n+=e.shift()}return n}function rt(e){let t;if(e.length>0&&Ye.test(e[0])){e.shift();t=O(e,Qe).trim();e.shift()}else{t=O(e,C)}return t}const ot="input, textarea, select";function it(e,t,n){const r=[];const o=et(t);do{O(o,Ze);const l=o.length;const u=O(o,/[,\[\s]/);if(u!==""){if(u==="every"){const c={trigger:"every"};O(o,Ze);c.pollInterval=d(O(o,/[,\[\s]/));O(o,Ze);var i=nt(e,o,"event");if(i){c.eventFilter=i}r.push(c)}else{const f={trigger:u};var i=nt(e,o,"event");if(i){f.eventFilter=i}O(o,Ze);while(o.length>0&&o[0]!==","){const a=o.shift();if(a==="changed"){f.changed=true}else if(a==="once"){f.once=true}else if(a==="consume"){f.consume=true}else if(a==="delay"&&o[0]===":"){o.shift();f.delay=d(O(o,C))}else if(a==="from"&&o[0]===":"){o.shift();if(Ye.test(o[0])){var s=rt(o)}else{var s=O(o,C);if(s==="closest"||s==="find"||s==="next"||s==="previous"){o.shift();const h=rt(o);if(h.length>0){s+=" "+h}}}f.from=s}else if(a==="target"&&o[0]===":"){o.shift();f.target=rt(o)}else if(a==="throttle"&&o[0]===":"){o.shift();f.throttle=d(O(o,C))}else if(a==="queue"&&o[0]===":"){o.shift();f.queue=O(o,C)}else if(a==="root"&&o[0]===":"){o.shift();f[a]=rt(o)}else if(a==="threshold"&&o[0]===":"){o.shift();f[a]=O(o,C)}else{fe(e,"htmx:syntax:error",{token:o.shift()})}O(o,Ze)}r.push(f)}}if(o.length===l){fe(e,"htmx:syntax:error",{token:o.shift()})}O(o,Ze)}while(o[0]===","&&o.shift());if(n){n[t]=r}return r}function st(e){const t=a(e,"hx-trigger");let n=[];if(t){const r=Q.config.triggerSpecsCache;n=r&&r[t]||it(e,t,r)}if(n.length>0){return n}else if(h(e,"form")){return[{trigger:"submit"}]}else if(h(e,'input[type="button"], input[type="submit"]')){return[{trigger:"click"}]}else if(h(e,ot)){return[{trigger:"change"}]}else{return[{trigger:"click"}]}}function lt(e){oe(e).cancelled=true}function ut(e,t,n){const r=oe(e);r.timeout=x().setTimeout(function(){if(se(e)&&r.cancelled!==true){if(!pt(n,e,Xt("hx:poll:trigger",{triggerSpec:n,target:e}))){t(e)}ut(e,t,n)}},n.pollInterval)}function ct(e){return location.hostname===e.hostname&&ee(e,"href")&&ee(e,"href").indexOf("#")!==0}function ft(e){return g(e,Q.config.disableSelector)}function at(t,n,e){if(t instanceof HTMLAnchorElement&&ct(t)&&(t.target===""||t.target==="_self")||t.tagName==="FORM"&&String(ee(t,"method")).toLowerCase()!=="dialog"){n.boosted=true;let r,o;if(t.tagName==="A"){r="get";o=ee(t,"href")}else{const i=ee(t,"method");r=i?i.toLowerCase():"get";o=ee(t,"action");if(o==null||o===""){o=location.href}if(r==="get"&&o.includes("?")){o=o.replace(/\?[^#]+/,"")}}e.forEach(function(e){gt(t,function(e,t){const n=ue(e);if(ft(n)){E(n);return}he(r,o,n,t)},n,e,true)})}}function ht(e,t){if(e.type==="submit"&&t.tagName==="FORM"){return true}else if(e.type==="click"){const n=t.closest('input[type="submit"], button');if(n&&n.form&&n.type==="submit"){return true}const r=t.closest("a");const o=/^#.+/;if(r&&r.href&&!o.test(r.getAttribute("href"))){return true}}return false}function dt(e,t){return oe(e).boosted&&e instanceof HTMLAnchorElement&&t.type==="click"&&(t.ctrlKey||t.metaKey)}function pt(e,t,n){const r=e.eventFilter;if(r){try{return r.call(t,n)!==true}catch(e){const o=r.source;fe(te().body,"htmx:eventFilter:error",{error:e,source:o});return true}}return false}function gt(l,u,e,c,f){const a=oe(l);let t;if(c.from){t=m(l,c.from)}else{t=[l]}if(c.changed){if(!("lastValue"in a)){a.lastValue=new WeakMap}t.forEach(function(e){if(!a.lastValue.has(c)){a.lastValue.set(c,new WeakMap)}a.lastValue.get(c).set(e,e.value)})}ie(t,function(i){const s=function(e){if(!se(l)){i.removeEventListener(c.trigger,s);return}if(dt(l,e)){return}if(f||ht(e,i)){e.preventDefault()}if(pt(c,l,e)){return}const t=oe(e);t.triggerSpec=c;if(t.handledFor==null){t.handledFor=[]}if(t.handledFor.indexOf(l)<0){t.handledFor.push(l);if(c.consume){e.stopPropagation()}if(c.target&&e.target){if(!h(ue(e.target),c.target)){return}}if(c.once){if(a.triggeredOnce){return}else{a.triggeredOnce=true}}if(c.changed){const n=e.target;const r=n.value;const o=a.lastValue.get(c);if(o.has(n)&&o.get(n)===r){return}o.set(n,r)}if(a.delayed){clearTimeout(a.delayed)}if(a.throttle){return}if(c.throttle>0){if(!a.throttle){ae(l,"htmx:trigger");u(l,e);a.throttle=x().setTimeout(function(){a.throttle=null},c.throttle)}}else if(c.delay>0){a.delayed=x().setTimeout(function(){ae(l,"htmx:trigger");u(l,e)},c.delay)}else{ae(l,"htmx:trigger");u(l,e)}}};if(e.listenerInfos==null){e.listenerInfos=[]}e.listenerInfos.push({trigger:c.trigger,listener:s,on:i});i.addEventListener(c.trigger,s)})}let mt=false;let yt=null;function xt(){if(!yt){yt=function(){mt=true};window.addEventListener("scroll",yt);window.addEventListener("resize",yt);setInterval(function(){if(mt){mt=false;ie(te().querySelectorAll("[hx-trigger*='revealed'],[data-hx-trigger*='revealed']"),function(e){bt(e)})}},200)}}function bt(e){if(!s(e,"data-hx-revealed")&&B(e)){e.setAttribute("data-hx-revealed","true");const t=oe(e);if(t.initHash){ae(e,"revealed")}else{e.addEventListener("htmx:afterProcessNode",function(){ae(e,"revealed")},{once:true})}}}function vt(e,t,n,r){const o=function(){if(!n.loaded){n.loaded=true;ae(e,"htmx:trigger");t(e)}};if(r>0){x().setTimeout(o,r)}else{o()}}function wt(t,n,e){let i=false;ie(de,function(r){if(s(t,"hx-"+r)){const o=a(t,"hx-"+r);i=true;n.path=o;n.verb=r;e.forEach(function(e){St(t,e,n,function(e,t){const n=ue(e);if(ft(n)){E(n);return}he(r,o,n,t)})})}});return i}function St(r,e,t,n){if(e.trigger==="revealed"){xt();gt(r,n,t,e);bt(ue(r))}else if(e.trigger==="intersect"){const o={};if(e.root){o.root=ce(r,e.root)}if(e.threshold){o.threshold=parseFloat(e.threshold)}const i=new IntersectionObserver(function(t){for(let e=0;e<t.length;e++){const n=t[e];if(n.isIntersecting){ae(r,"intersect");break}}},o);i.observe(ue(r));gt(ue(r),n,t,e)}else if(!t.firstInitCompleted&&e.trigger==="load"){if(!pt(e,r,Xt("load",{elt:r}))){vt(ue(r),n,t,e.delay)}}else if(e.pollInterval>0){t.polling=true;ut(ue(r),n,e)}else{gt(r,n,t,e)}}function Et(e){const t=ue(e);if(!t){return false}const n=t.attributes;for(let e=0;e<n.length;e++){const r=n[e].name;if(l(r,"hx-on:")||l(r,"data-hx-on:")||l(r,"hx-on-")||l(r,"data-hx-on-")){return true}}return false}const Ct=(new XPathEvaluator).createExpression('.//*[@*[ starts-with(name(), "hx-on:") or starts-with(name(), "data-hx-on:") or'+' starts-with(name(), "hx-on-") or starts-with(name(), "data-hx-on-") ]]');function Ot(e,t){if(Et(e)){t.push(ue(e))}const n=Ct.evaluate(e);let r=null;while(r=n.iterateNext())t.push(ue(r))}function Ht(e){const t=[];if(e instanceof DocumentFragment){for(const n of e.childNodes){Ot(n,t)}}else{Ot(e,t)}return t}function Tt(e){if(e.querySelectorAll){const n=", [hx-boost] a, [data-hx-boost] a, a[hx-boost], a[data-hx-boost]";const r=[];for(const i in jn){const s=jn[i];if(s.getSelectors){var t=s.getSelectors();if(t){r.push(t)}}}const o=e.querySelectorAll(R+n+", form, [type='submit'],"+" [hx-ext], [data-hx-ext], [hx-trigger], [data-hx-trigger]"+r.flat().map(e=>", "+e).join(""));return o}else{return[]}}function Rt(e){const t=At(e.target);const n=It(e);if(n){n.lastButtonClicked=t}}function qt(e){const t=It(e);if(t){t.lastButtonClicked=null}}function At(e){return g(ue(e),"button, input[type='submit']")}function Nt(e){return e.form||g(e,"form")}function It(e){const t=At(e.target);if(!t){return}const n=Nt(t);if(!n){return}return oe(n)}function Lt(e){e.addEventListener("click",Rt);e.addEventListener("focusin",Rt);e.addEventListener("focusout",qt)}function Dt(t,e,n){const r=oe(t);if(!Array.isArray(r.onHandlers)){r.onHandlers=[]}let o;const i=function(e){On(t,function(){if(ft(t)){return}if(!o){o=new Function("event",n)}o.call(t,e)})};t.addEventListener(e,i);r.onHandlers.push({event:e,listener:i})}function Pt(t){De(t);for(let e=0;e<t.attributes.length;e++){const n=t.attributes[e].name;const r=t.attributes[e].value;if(l(n,"hx-on")||l(n,"data-hx-on")){const o=n.indexOf("-on")+3;const i=n.slice(o,o+1);if(i==="-"||i===":"){let e=n.slice(o+1);if(l(e,":")){e="htmx"+e}else if(l(e,"-")){e="htmx:"+e.slice(1)}else if(l(e,"htmx-")){e="htmx:"+e.slice(5)}Dt(t,e,r)}}}}function kt(t){ae(t,"htmx:beforeProcessNode");const n=oe(t);const e=st(t);const r=wt(t,n,e);if(!r){if(ne(t,"hx-boost")==="true"){at(t,n,e)}else if(s(t,"hx-trigger")){e.forEach(function(e){St(t,e,n,function(){})})}}if(t.tagName==="FORM"||ee(t,"type")==="submit"&&s(t,"form")){Lt(t)}n.firstInitCompleted=true;ae(t,"htmx:afterProcessNode")}function Mt(e){if(!(e instanceof Element)){return false}const t=oe(e);const n=Le(e);if(t.initHash!==n){Pe(e);t.initHash=n;return true}return false}function Ft(e){e=S(e);if(ft(e)){E(e);return}const t=[];if(Mt(e)){t.push(e)}ie(Tt(e),function(e){if(ft(e)){E(e);return}if(Mt(e)){t.push(e)}});ie(Ht(e),Pt);ie(t,kt)}function Bt(e){return e.replace(/([a-z0-9])([A-Z])/g,"$1-$2").toLowerCase()}function Xt(e,t){return new CustomEvent(e,{bubbles:true,cancelable:true,composed:true,detail:t})}function fe(e,t,n){ae(e,t,le({error:t},n))}function Ut(e){return e==="htmx:afterProcessNode"}function Vt(e,t,n){ie(Jn(e,[],n),function(e){try{t(e)}catch(e){H(e)}})}function H(e){console.error(e)}function ae(e,t,n){e=S(e);if(n==null){n={}}n.elt=e;const r=Xt(t,n);if(Q.logger&&!Ut(t)){Q.logger(e,t,n)}if(n.error){H(n.error+(n.target?", "+n.target:""));ae(e,"htmx:error",{errorInfo:n})}let o=e.dispatchEvent(r);const i=Bt(t);if(o&&i!==t){const s=Xt(i,r.detail);o=o&&e.dispatchEvent(s)}Vt(ue(e),function(e){o=o&&(e.onEvent(t,r)!==false&&!r.defaultPrevented)});return o}let jt;function $t(e){jt=e;if(U()){sessionStorage.setItem("htmx-current-path-for-history",e)}}$t(location.pathname+location.search);function _t(){const e=te().querySelector("[hx-history-elt],[data-hx-history-elt]");return e||te().body}function zt(t,e){if(!U()){return}const n=Kt(e);const r=te().title;const o=window.scrollY;if(Q.config.historyCacheSize<=0){sessionStorage.removeItem("htmx-history-cache");return}t=V(t);const i=v(sessionStorage.getItem("htmx-history-cache"))||[];for(let e=0;e<i.length;e++){if(i[e].url===t){i.splice(e,1);break}}const s={url:t,content:n,title:r,scroll:o};ae(te().body,"htmx:historyItemCreated",{item:s,cache:i});i.push(s);while(i.length>Q.config.historyCacheSize){i.shift()}while(i.length>0){try{sessionStorage.setItem("htmx-history-cache",JSON.stringify(i));break}catch(e){fe(te().body,"htmx:historyCacheError",{cause:e,cache:i});i.shift()}}}function Jt(t){if(!U()){return null}t=V(t);const n=v(sessionStorage.getItem("htmx-history-cache"))||[];for(let e=0;e<n.length;e++){if(n[e].url===t){return n[e]}}return null}function Kt(e){const t=Q.config.requestClass;const n=e.cloneNode(true);ie(y(n,"."+t),function(e){b(e,t)});ie(y(n,"[data-disabled-by-htmx]"),function(e){e.removeAttribute("disabled")});return n.innerHTML}function Gt(){const e=_t();let t=jt;if(U()){t=sessionStorage.getItem("htmx-current-path-for-history")}t=t||location.pathname+location.search;const n=te().querySelector('[hx-history="false" i],[data-hx-history="false" i]');if(!n){ae(te().body,"htmx:beforeHistorySave",{path:t,historyElt:e});zt(t,e)}if(Q.config.historyEnabled)history.replaceState({htmx:true},te().title,location.href)}function Wt(e){if(Q.config.getCacheBusterParam){e=e.replace(/org\.htmx\.cache-buster=[^&]*&?/,"");if(Z(e,"&")||Z(e,"?")){e=e.slice(0,-1)}}if(Q.config.historyEnabled){history.pushState({htmx:true},"",e)}$t(e)}function Zt(e){if(Q.config.historyEnabled)history.replaceState({htmx:true},"",e);$t(e)}function Yt(e){ie(e,function(e){e.call(undefined)})}function Qt(e){const t=new XMLHttpRequest;const n={swapStyle:"innerHTML",swapDelay:0,settleDelay:0};const r={path:e,xhr:t,historyElt:_t(),swapSpec:n};t.open("GET",e,true);if(Q.config.historyRestoreAsHxRequest){t.setRequestHeader("HX-Request","true")}t.setRequestHeader("HX-History-Restore-Request","true");t.setRequestHeader("HX-Current-URL",location.href);t.onload=function(){if(this.status>=200&&this.status<400){r.response=this.response;ae(te().body,"htmx:historyCacheMissLoad",r);_e(r.historyElt,r.response,n,{contextElement:r.historyElt,historyRequest:true});$t(r.path);ae(te().body,"htmx:historyRestore",{path:e,cacheMiss:true,serverResponse:r.response})}else{fe(te().body,"htmx:historyCacheMissLoadError",r)}};if(ae(te().body,"htmx:historyCacheMiss",r)){t.send()}}function en(e){Gt();e=e||location.pathname+location.search;const t=Jt(e);if(t){const n={swapStyle:"innerHTML",swapDelay:0,settleDelay:0,scroll:t.scroll};const r={path:e,item:t,historyElt:_t(),swapSpec:n};if(ae(te().body,"htmx:historyCacheHit",r)){_e(r.historyElt,t.content,n,{contextElement:r.historyElt,title:t.title});$t(r.path);ae(te().body,"htmx:historyRestore",r)}}else{if(Q.config.refreshOnHistoryMiss){Q.location.reload(true)}else{Qt(e)}}}function tn(e){let t=ve(e,"hx-indicator");if(t==null){t=[e]}ie(t,function(e){const t=oe(e);t.requestCount=(t.requestCount||0)+1;w(e,Q.config.requestClass)});return t}function nn(e){let t=ve(e,"hx-disabled-elt");if(t==null){t=[]}ie(t,function(e){const t=oe(e);t.requestCount=(t.requestCount||0)+1;if(!e.hasAttribute("disabled")){e.setAttribute("disabled","");e.setAttribute("data-disabled-by-htmx","")}});return t}function rn(e,t){ie(e.concat(t),function(e){const t=oe(e);t.requestCount=(t.requestCount||1)-1});ie(e,function(e){const t=oe(e);if(t.requestCount===0){b(e,Q.config.requestClass)}});ie(t,function(e){const t=oe(e);if(t.requestCount===0&&e.hasAttribute("data-disabled-by-htmx")){e.removeAttribute("disabled");e.removeAttribute("data-disabled-by-htmx")}})}function on(t,n){for(let e=0;e<t.length;e++){const r=t[e];if(r.isSameNode(n)){return true}}return false}function sn(e){const t=e;if(t.name===""||t.name==null||t.disabled||g(t,"fieldset[disabled]")){return false}if(t.type==="button"||t.type==="submit"||t.tagName==="image"||t.tagName==="reset"||t.tagName==="file"){return false}if(t.type==="checkbox"||t.type==="radio"){return t.checked}return true}function ln(t,e,n){if(t!=null&&e!=null){if(Array.isArray(e)){e.forEach(function(e){n.append(t,e)})}else{n.append(t,e)}}}function un(t,n,r){if(t!=null&&n!=null){let e=r.getAll(t);if(Array.isArray(n)){e=e.filter(e=>n.indexOf(e)<0)}else{e=e.filter(e=>e!==n)}r.delete(t);ie(e,e=>r.append(t,e))}}function cn(e){if(e instanceof HTMLSelectElement&&e.multiple){return F(e.querySelectorAll("option:checked")).map(function(e){return e.value})}if(e instanceof HTMLInputElement&&e.files){return F(e.files)}return e.value}function fn(t,n,r,e,o){if(e==null||on(t,e)){return}else{t.push(e)}if(sn(e)){const i=ee(e,"name");ln(i,cn(e),n);if(o){an(e,r)}}if(e instanceof HTMLFormElement){ie(e.elements,function(e){if(t.indexOf(e)>=0){un(e.name,cn(e),n)}else{t.push(e)}if(o){an(e,r)}});new FormData(e).forEach(function(e,t){if(e instanceof File&&e.name===""){return}ln(t,e,n)})}}function an(e,t){const n=e;if(n.willValidate){ae(n,"htmx:validation:validate");if(!n.checkValidity()){if(ae(n,"htmx:validation:failed",{message:n.validationMessage,validity:n.validity})&&!t.length&&Q.config.reportValidityOfForms){n.reportValidity()}t.push({elt:n,message:n.validationMessage,validity:n.validity})}}}function hn(n,e){for(const t of e.keys()){n.delete(t)}e.forEach(function(e,t){n.append(t,e)});return n}function dn(e,t){const n=[];const r=new FormData;const o=new FormData;const i=[];const s=oe(e);if(s.lastButtonClicked&&!se(s.lastButtonClicked)){s.lastButtonClicked=null}let l=e instanceof HTMLFormElement&&e.noValidate!==true||a(e,"hx-validate")==="true";if(s.lastButtonClicked){l=l&&s.lastButtonClicked.formNoValidate!==true}if(t!=="get"){fn(n,o,i,Nt(e),l)}fn(n,r,i,e,l);if(s.lastButtonClicked||e.tagName==="BUTTON"||e.tagName==="INPUT"&&ee(e,"type")==="submit"){const c=s.lastButtonClicked||e;const f=ee(c,"name");ln(f,c.value,o)}const u=ve(e,"hx-include");ie(u,function(e){fn(n,r,i,ue(e),l);if(!h(e,"form")){ie(p(e).querySelectorAll(ot),function(e){fn(n,r,i,e,l)})}});hn(r,o);return{errors:i,formData:r,values:kn(r)}}function pn(e,t,n){if(e!==""){e+="&"}if(String(n)==="[object Object]"){n=JSON.stringify(n)}const r=encodeURIComponent(n);e+=encodeURIComponent(t)+"="+r;return e}function gn(e){e=Dn(e);let n="";e.forEach(function(e,t){n=pn(n,t,e)});return n}function mn(e,t,n){const r={"HX-Request":"true","HX-Trigger":ee(e,"id"),"HX-Trigger-Name":ee(e,"name"),"HX-Target":a(t,"id"),"HX-Current-URL":location.href};Cn(e,"hx-headers",false,r);if(n!==undefined){r["HX-Prompt"]=n}if(oe(e).boosted){r["HX-Boosted"]="true"}return r}function yn(n,e){const t=ne(e,"hx-params");if(t){if(t==="none"){return new FormData}else if(t==="*"){return n}else if(t.indexOf("not ")===0){ie(t.slice(4).split(","),function(e){e=e.trim();n.delete(e)});return n}else{const r=new FormData;ie(t.split(","),function(t){t=t.trim();if(n.has(t)){n.getAll(t).forEach(function(e){r.append(t,e)})}});return r}}else{return n}}function xn(e){return!!ee(e,"href")&&ee(e,"href").indexOf("#")>=0}function bn(e,t){const n=t||ne(e,"hx-swap");const r={swapStyle:oe(e).boosted?"innerHTML":Q.config.defaultSwapStyle,swapDelay:Q.config.defaultSwapDelay,settleDelay:Q.config.defaultSettleDelay};if(Q.config.scrollIntoViewOnBoost&&oe(e).boosted&&!xn(e)){r.show="top"}if(n){const s=X(n);if(s.length>0){for(let e=0;e<s.length;e++){const l=s[e];if(l.indexOf("swap:")===0){r.swapDelay=d(l.slice(5))}else if(l.indexOf("settle:")===0){r.settleDelay=d(l.slice(7))}else if(l.indexOf("transition:")===0){r.transition=l.slice(11)==="true"}else if(l.indexOf("ignoreTitle:")===0){r.ignoreTitle=l.slice(12)==="true"}else if(l.indexOf("scroll:")===0){const u=l.slice(7);var o=u.split(":");const c=o.pop();var i=o.length>0?o.join(":"):null;r.scroll=c;r.scrollTarget=i}else if(l.indexOf("show:")===0){const f=l.slice(5);var o=f.split(":");const a=o.pop();var i=o.length>0?o.join(":"):null;r.show=a;r.showTarget=i}else if(l.indexOf("focus-scroll:")===0){const h=l.slice("focus-scroll:".length);r.focusScroll=h=="true"}else if(e==0){r.swapStyle=l}else{H("Unknown modifier in hx-swap: "+l)}}}}return r}function vn(e){return ne(e,"hx-encoding")==="multipart/form-data"||h(e,"form")&&ee(e,"enctype")==="multipart/form-data"}function wn(t,n,r){let o=null;Vt(n,function(e){if(o==null){o=e.encodeParameters(t,r,n)}});if(o!=null){return o}else{if(vn(n)){return hn(new FormData,Dn(r))}else{return gn(r)}}}function Sn(e){return{tasks:[],elts:[e]}}function En(e,t){const n=e[0];const r=e[e.length-1];if(t.scroll){var o=null;if(t.scrollTarget){o=ue(ce(n,t.scrollTarget))}if(t.scroll==="top"&&(n||o)){o=o||n;o.scrollTop=0}if(t.scroll==="bottom"&&(r||o)){o=o||r;o.scrollTop=o.scrollHeight}if(typeof t.scroll==="number"){x().setTimeout(function(){window.scrollTo(0,t.scroll)},0)}}if(t.show){var o=null;if(t.showTarget){let e=t.showTarget;if(t.showTarget==="window"){e="body"}o=ue(ce(n,e))}if(t.show==="top"&&(n||o)){o=o||n;o.scrollIntoView({block:"start",behavior:Q.config.scrollBehavior})}if(t.show==="bottom"&&(r||o)){o=o||r;o.scrollIntoView({block:"end",behavior:Q.config.scrollBehavior})}}}function Cn(r,e,o,i,s){if(i==null){i={}}if(r==null){return i}const l=a(r,e);if(l){let e=l.trim();let t=o;if(e==="unset"){return null}if(e.indexOf("javascript:")===0){e=e.slice(11);t=true}else if(e.indexOf("js:")===0){e=e.slice(3);t=true}if(e.indexOf("{")!==0){e="{"+e+"}"}let n;if(t){n=On(r,function(){if(s){return Function("event","return ("+e+")").call(r,s)}else{return Function("return ("+e+")").call(r)}},{})}else{n=v(e)}for(const u in n){if(n.hasOwnProperty(u)){if(i[u]==null){i[u]=n[u]}}}}return Cn(ue(c(r)),e,o,i,s)}function On(e,t,n){if(Q.config.allowEval){return t()}else{fe(e,"htmx:evalDisallowedError");return n}}function Hn(e,t,n){return Cn(e,"hx-vars",true,n,t)}function Tn(e,t,n){return Cn(e,"hx-vals",false,n,t)}function Rn(e,t){return le(Hn(e,t),Tn(e,t))}function qn(t,n,r){if(r!==null){try{t.setRequestHeader(n,r)}catch(e){t.setRequestHeader(n,encodeURIComponent(r));t.setRequestHeader(n+"-URI-AutoEncoded","true")}}}function An(t){if(t.responseURL){try{const e=new URL(t.responseURL);return e.pathname+e.search}catch(e){fe(te().body,"htmx:badResponseUrl",{url:t.responseURL})}}}function T(e,t){return t.test(e.getAllResponseHeaders())}function Nn(t,n,r){t=t.toLowerCase();if(r){if(r instanceof Element||typeof r==="string"){return he(t,n,null,null,{targetOverride:S(r)||be,returnPromise:true})}else{let e=S(r.target);if(r.target&&!e||r.source&&!e&&!S(r.source)){e=be}return he(t,n,S(r.source),r.event,{handler:r.handler,headers:r.headers,values:r.values,targetOverride:e,swapOverride:r.swap,select:r.select,returnPromise:true,push:r.push,replace:r.replace,selectOOB:r.selectOOB})}}else{return he(t,n,null,null,{returnPromise:true})}}function In(e){const t=[];while(e){t.push(e);e=e.parentElement}return t}function Ln(e,t,n){const r=new URL(t,location.protocol!=="about:"?location.href:window.origin);const o=location.protocol!=="about:"?location.origin:window.origin;const i=o===r.origin;if(Q.config.selfRequestsOnly){if(!i){return false}}return ae(e,"htmx:validateUrl",le({url:r,sameHost:i},n))}function Dn(e){if(e instanceof FormData)return e;const t=new FormData;for(const n in e){if(e.hasOwnProperty(n)){if(e[n]&&typeof e[n].forEach==="function"){e[n].forEach(function(e){t.append(n,e)})}else if(typeof e[n]==="object"&&!(e[n]instanceof Blob)){t.append(n,JSON.stringify(e[n]))}else{t.append(n,e[n])}}}return t}function Pn(r,o,e){return new Proxy(e,{get:function(t,e){if(typeof e==="number")return t[e];if(e==="length")return t.length;if(e==="push"){return function(e){t.push(e);r.append(o,e)}}if(typeof t[e]==="function"){return function(){t[e].apply(t,arguments);r.delete(o);t.forEach(function(e){r.append(o,e)})}}if(t[e]&&t[e].length===1){return t[e][0]}else{return t[e]}},set:function(e,t,n){e[t]=n;r.delete(o);e.forEach(function(e){r.append(o,e)});return true}})}function kn(o){return new Proxy(o,{get:function(e,t){if(typeof t==="symbol"){const r=Reflect.get(e,t);if(typeof r==="function"){return function(){return r.apply(o,arguments)}}else{return r}}if(t==="toJSON"){return()=>Object.fromEntries(o)}if(t in e){if(typeof e[t]==="function"){return function(){return o[t].apply(o,arguments)}}}const n=o.getAll(t);if(n.length===0){return undefined}else if(n.length===1){return n[0]}else{return Pn(e,t,n)}},set:function(t,n,e){if(typeof n!=="string"){return false}t.delete(n);if(e&&typeof e.forEach==="function"){e.forEach(function(e){t.append(n,e)})}else if(typeof e==="object"&&!(e instanceof Blob)){t.append(n,JSON.stringify(e))}else{t.append(n,e)}return true},deleteProperty:function(e,t){if(typeof t==="string"){e.delete(t)}return true},ownKeys:function(e){return Reflect.ownKeys(Object.fromEntries(e))},getOwnPropertyDescriptor:function(e,t){return Reflect.getOwnPropertyDescriptor(Object.fromEntries(e),t)}})}function he(t,n,r,o,i,k){let s=null;let l=null;i=i!=null?i:{};if(i.returnPromise&&typeof Promise!=="undefined"){var e=new Promise(function(e,t){s=e;l=t})}if(r==null){r=te().body}const M=i.handler||Vn;const F=i.select||null;if(!se(r)){re(s);return e}const u=i.targetOverride||ue(Se(r));if(u==null||u==be){fe(r,"htmx:targetError",{target:ne(r,"hx-target")});re(l);return e}let c=oe(r);const f=c.lastButtonClicked;if(f){const A=ee(f,"formaction");if(A!=null){n=A}const N=ee(f,"formmethod");if(N!=null){if(de.includes(N.toLowerCase())){t=N}else{re(s);return e}}}const a=ne(r,"hx-confirm");if(k===undefined){const K=function(e){return he(t,n,r,o,i,!!e)};const G={target:u,elt:r,path:n,verb:t,triggeringEvent:o,etc:i,issueRequest:K,question:a};if(ae(r,"htmx:confirm",G)===false){re(s);return e}}let h=r;let d=ne(r,"hx-sync");let p=null;let B=false;if(d){const I=d.split(":");const L=I[0].trim();if(L==="this"){h=we(r,"hx-sync")}else{h=ue(ce(r,L))}d=(I[1]||"drop").trim();c=oe(h);if(d==="drop"&&c.xhr&&c.abortable!==true){re(s);return e}else if(d==="abort"){if(c.xhr){re(s);return e}else{B=true}}else if(d==="replace"){ae(h,"htmx:abort")}else if(d.indexOf("queue")===0){const W=d.split(" ");p=(W[1]||"last").trim()}}if(c.xhr){if(c.abortable){ae(h,"htmx:abort")}else{if(p==null){if(o){const D=oe(o);if(D&&D.triggerSpec&&D.triggerSpec.queue){p=D.triggerSpec.queue}}if(p==null){p="last"}}if(c.queuedRequests==null){c.queuedRequests=[]}if(p==="first"&&c.queuedRequests.length===0){c.queuedRequests.push(function(){he(t,n,r,o,i)})}else if(p==="all"){c.queuedRequests.push(function(){he(t,n,r,o,i)})}else if(p==="last"){c.queuedRequests=[];c.queuedRequests.push(function(){he(t,n,r,o,i)})}re(s);return e}}const g=new XMLHttpRequest;c.xhr=g;c.abortable=B;const m=function(){c.xhr=null;c.abortable=false;if(c.queuedRequests!=null&&c.queuedRequests.length>0){const e=c.queuedRequests.shift();e()}};const X=ne(r,"hx-prompt");if(X){var y=prompt(X);if(y===null||!ae(r,"htmx:prompt",{prompt:y,target:u})){re(s);m();return e}}if(a&&!k){if(!confirm(a)){re(s);m();return e}}let x=mn(r,u,y);if(t!=="get"&&!vn(r)){x["Content-Type"]="application/x-www-form-urlencoded"}if(i.headers){x=le(x,i.headers)}const U=dn(r,t);let b=U.errors;const V=U.formData;if(i.values){hn(V,Dn(i.values))}const j=Dn(Rn(r,o));const v=hn(V,j);let w=yn(v,r);if(Q.config.getCacheBusterParam&&t==="get"){w.set("org.htmx.cache-buster",ee(u,"id")||"true")}if(n==null||n===""){n=location.href}const S=Cn(r,"hx-request");const $=oe(r).boosted;let E=Q.config.methodsThatUseUrlParams.indexOf(t)>=0;const C={boosted:$,useUrlParams:E,formData:w,parameters:kn(w),unfilteredFormData:v,unfilteredParameters:kn(v),headers:x,elt:r,target:u,verb:t,errors:b,withCredentials:i.credentials||S.credentials||Q.config.withCredentials,timeout:i.timeout||S.timeout||Q.config.timeout,path:n,triggeringEvent:o};if(!ae(r,"htmx:configRequest",C)){re(s);m();return e}n=C.path;t=C.verb;x=C.headers;w=Dn(C.parameters);b=C.errors;E=C.useUrlParams;if(b&&b.length>0){ae(r,"htmx:validation:halted",C);re(s);m();return e}const _=n.split("#");const z=_[0];const O=_[1];let H=n;if(E){H=z;const Z=!w.keys().next().done;if(Z){if(H.indexOf("?")<0){H+="?"}else{H+="&"}H+=gn(w);if(O){H+="#"+O}}}if(!Ln(r,H,C)){fe(r,"htmx:invalidPath",C);re(l);m();return e}g.open(t.toUpperCase(),H,true);g.overrideMimeType("text/html");g.withCredentials=C.withCredentials;g.timeout=C.timeout;if(S.noHeaders){}else{for(const P in x){if(x.hasOwnProperty(P)){const Y=x[P];qn(g,P,Y)}}}const T={xhr:g,target:u,requestConfig:C,etc:i,boosted:$,select:F,pathInfo:{requestPath:n,finalRequestPath:H,responsePath:null,anchor:O}};g.onload=function(){try{const t=In(r);T.pathInfo.responsePath=An(g);M(r,T);if(T.keepIndicators!==true){rn(R,q)}ae(r,"htmx:afterRequest",T);ae(r,"htmx:afterOnLoad",T);if(!se(r)){let e=null;while(t.length>0&&e==null){const n=t.shift();if(se(n)){e=n}}if(e){ae(e,"htmx:afterRequest",T);ae(e,"htmx:afterOnLoad",T)}}re(s)}catch(e){fe(r,"htmx:onLoadError",le({error:e},T));throw e}finally{m()}};g.onerror=function(){rn(R,q);fe(r,"htmx:afterRequest",T);fe(r,"htmx:sendError",T);re(l);m()};g.onabort=function(){rn(R,q);fe(r,"htmx:afterRequest",T);fe(r,"htmx:sendAbort",T);re(l);m()};g.ontimeout=function(){rn(R,q);fe(r,"htmx:afterRequest",T);fe(r,"htmx:timeout",T);re(l);m()};if(!ae(r,"htmx:beforeRequest",T)){re(s);m();return e}var R=tn(r);var q=nn(r);ie(["loadstart","loadend","progress","abort"],function(t){ie([g,g.upload],function(e){e.addEventListener(t,function(e){ae(r,"htmx:xhr:"+t,{lengthComputable:e.lengthComputable,loaded:e.loaded,total:e.total})})})});ae(r,"htmx:beforeSend",T);const J=E?null:wn(g,r,w);g.send(J);return e}function Mn(e,t){const n=t.xhr;let r=null;let o=null;if(T(n,/HX-Push:/i)){r=n.getResponseHeader("HX-Push");o="push"}else if(T(n,/HX-Push-Url:/i)){r=n.getResponseHeader("HX-Push-Url");o="push"}else if(T(n,/HX-Replace-Url:/i)){r=n.getResponseHeader("HX-Replace-Url");o="replace"}if(r){if(r==="false"){return{}}else{return{type:o,path:r}}}const i=t.pathInfo.finalRequestPath;const s=t.pathInfo.responsePath;let l=t.etc.push||ne(e,"hx-push-url");let u=t.etc.replace||ne(e,"hx-replace-url");if(l==="false")l=null;if(u==="false")u=null;const c=oe(e).boosted;let f=null;let a=null;if(l){f="push";a=l}else if(u){f="replace";a=u}else if(c){f="push";a=s||i}if(a){if(a==="true"){a=s||i}if(t.pathInfo.anchor&&a.indexOf("#")===-1){a=a+"#"+t.pathInfo.anchor}return{type:f,path:a}}else{return{}}}function Fn(e,t){var n=new RegExp(e.code);return n.test(t.toString(10))}function Bn(e){for(var t=0;t<Q.config.responseHandling.length;t++){var n=Q.config.responseHandling[t];if(Fn(n,e.status)){return n}}return{swap:false}}function Xn(e){if(e){const t=f("title");if(t){t.textContent=e}else{window.document.title=e}}}function Un(e,t){if(t==="this"){return e}const n=ue(ce(e,t));if(n==null){fe(e,"htmx:targetError",{target:t});throw new Error(`Invalid re-target ${t}`)}return n}function Vn(t,e){const n=e.xhr;let r=e.target;const o=e.etc;const i=e.select;if(!ae(t,"htmx:beforeOnLoad",e))return;if(T(n,/HX-Trigger:/i)){ze(n,"HX-Trigger",t)}if(T(n,/HX-Location:/i)){let e=n.getResponseHeader("HX-Location");var s={};if(e.indexOf("{")===0){s=v(e);e=s.path;delete s.path}s.push=s.push??"true";Nn("get",e,s);return}const l=T(n,/HX-Refresh:/i)&&n.getResponseHeader("HX-Refresh")==="true";if(T(n,/HX-Redirect:/i)){e.keepIndicators=true;Q.location.href=n.getResponseHeader("HX-Redirect");l&&Q.location.reload();return}if(l){e.keepIndicators=true;Q.location.reload();return}const u=Mn(t,e);const c=Bn(n);const f=c.swap;let a=!!c.error;let h=Q.config.ignoreTitle||c.ignoreTitle;let d=c.select;if(c.target){e.target=Un(t,c.target)}var p=o.swapOverride;if(p==null&&c.swapOverride){p=c.swapOverride}if(T(n,/HX-Retarget:/i)){e.target=Un(t,n.getResponseHeader("HX-Retarget"))}if(T(n,/HX-Reswap:/i)){p=n.getResponseHeader("HX-Reswap")}var g=n.response;var m=le({shouldSwap:f,serverResponse:g,isError:a,ignoreTitle:h,selectOverride:d,swapOverride:p},e);if(c.event&&!ae(r,c.event,m))return;if(!ae(r,"htmx:beforeSwap",m))return;r=m.target;g=m.serverResponse;a=m.isError;h=m.ignoreTitle;d=m.selectOverride;p=m.swapOverride;e.target=r;e.failed=a;e.successful=!a;if(m.shouldSwap){if(n.status===286){lt(t)}Vt(t,function(e){g=e.transformResponse(g,n,t)});if(u.type){Gt()}var y=bn(t,p);if(!y.hasOwnProperty("ignoreTitle")){y.ignoreTitle=h}w(r,Q.config.swappingClass);if(i){d=i}if(T(n,/HX-Reselect:/i)){d=n.getResponseHeader("HX-Reselect")}const x=o.selectOOB||ne(t,"hx-select-oob");const b=ne(t,"hx-select");_e(r,g,y,{select:d==="unset"?null:d||b,selectOOB:x,eventInfo:e,anchor:e.pathInfo.anchor,contextElement:t,afterSwapCallback:function(){if(T(n,/HX-Trigger-After-Swap:/i)){let e=t;if(!se(t)){e=te().body}ze(n,"HX-Trigger-After-Swap",e)}},afterSettleCallback:function(){if(T(n,/HX-Trigger-After-Settle:/i)){let e=t;if(!se(t)){e=te().body}ze(n,"HX-Trigger-After-Settle",e)}},beforeSwapCallback:function(){if(u.type){ae(te().body,"htmx:beforeHistoryUpdate",le({history:u},e));if(u.type==="push"){Wt(u.path);ae(te().body,"htmx:pushedIntoHistory",{path:u.path})}else{Zt(u.path);ae(te().body,"htmx:replacedInHistory",{path:u.path})}}}})}if(a){fe(t,"htmx:responseError",le({error:"Response Status Error Code "+n.status+" from "+e.pathInfo.requestPath},e))}}const jn={};function $n(){return{init:function(e){return null},getSelectors:function(){return null},onEvent:function(e,t){return true},transformResponse:function(e,t,n){return e},isInlineSwap:function(e){return false},handleSwap:function(e,t,n,r){return false},encodeParameters:function(e,t,n){return null}}}function _n(e,t){if(t.init){t.init(n)}jn[e]=le($n(),t)}function zn(e){delete jn[e]}function Jn(e,n,r){if(n==undefined){n=[]}if(e==undefined){return n}if(r==undefined){r=[]}const t=a(e,"hx-ext");if(t){ie(t.split(","),function(e){e=e.replace(/ /g,"");if(e.slice(0,7)=="ignore:"){r.push(e.slice(7));return}if(r.indexOf(e)<0){const t=jn[e];if(t&&n.indexOf(t)<0){n.push(t)}}})}return Jn(ue(c(e)),n,r)}var Kn=false;te().addEventListener("DOMContentLoaded",function(){Kn=true});function Gn(e){if(Kn||te().readyState==="complete"){e()}else{te().addEventListener("DOMContentLoaded",e)}}function Wn(){if(Q.config.includeIndicatorStyles!==false){const e=Q.config.inlineStyleNonce?` nonce="${Q.config.inlineStyleNonce}"`:"";const t=Q.config.indicatorClass;const n=Q.config.requestClass;te().head.insertAdjacentHTML("beforeend",`<style${e}>`+`.${t}{opacity:0;visibility: hidden} `+`.${n} .${t}, .${n}.${t}{opacity:1;visibility: visible;transition: opacity 200ms ease-in}`+"</style>")}}function Zn(){const e=te().querySelector('meta[name="htmx-config"]');if(e){return v(e.content)}else{return null}}function Yn(){const e=Zn();if(e){Q.config=le(Q.config,e)}}Gn(function(){Yn();Wn();let e=te().body;Ft(e);const t=te().querySelectorAll("[hx-trigger='restored'],[data-hx-trigger='restored']");e.addEventListener("htmx:abort",function(e){const t=e.detail.elt||e.target;const n=oe(t);if(n&&n.xhr){n.xhr.abort()}});const n=window.onpopstate?window.onpopstate.bind(window):null;window.onpopstate=function(e){if(e.state&&e.state.htmx){en();ie(t,function(e){ae(e,"htmx:restored",{document:te(),triggerEvent:ae})})}else{if(n){n(e)}}};x().setTimeout(function(){ae(e,"htmx:load",{});e=null},0)});return Q}();
;
/*
Stimulus 3.2.1
Copyright © 2023 Basecamp, LLC
 */
(function (global, factory) {
    typeof exports === 'object' && typeof module !== 'undefined' ? factory(exports) :
    typeof define === 'function' && define.amd ? define(['exports'], factory) :
    (global = typeof globalThis !== 'undefined' ? globalThis : global || self, factory(global.Stimulus = {}));
}(this, (function (exports) { 'use strict';

    class EventListener {
        constructor(eventTarget, eventName, eventOptions) {
            this.eventTarget = eventTarget;
            this.eventName = eventName;
            this.eventOptions = eventOptions;
            this.unorderedBindings = new Set();
        }
        connect() {
            this.eventTarget.addEventListener(this.eventName, this, this.eventOptions);
        }
        disconnect() {
            this.eventTarget.removeEventListener(this.eventName, this, this.eventOptions);
        }
        bindingConnected(binding) {
            this.unorderedBindings.add(binding);
        }
        bindingDisconnected(binding) {
            this.unorderedBindings.delete(binding);
        }
        handleEvent(event) {
            const extendedEvent = extendEvent(event);
            for (const binding of this.bindings) {
                if (extendedEvent.immediatePropagationStopped) {
                    break;
                }
                else {
                    binding.handleEvent(extendedEvent);
                }
            }
        }
        hasBindings() {
            return this.unorderedBindings.size > 0;
        }
        get bindings() {
            return Array.from(this.unorderedBindings).sort((left, right) => {
                const leftIndex = left.index, rightIndex = right.index;
                return leftIndex < rightIndex ? -1 : leftIndex > rightIndex ? 1 : 0;
            });
        }
    }
    function extendEvent(event) {
        if ("immediatePropagationStopped" in event) {
            return event;
        }
        else {
            const { stopImmediatePropagation } = event;
            return Object.assign(event, {
                immediatePropagationStopped: false,
                stopImmediatePropagation() {
                    this.immediatePropagationStopped = true;
                    stopImmediatePropagation.call(this);
                },
            });
        }
    }

    class Dispatcher {
        constructor(application) {
            this.application = application;
            this.eventListenerMaps = new Map();
            this.started = false;
        }
        start() {
            if (!this.started) {
                this.started = true;
                this.eventListeners.forEach((eventListener) => eventListener.connect());
            }
        }
        stop() {
            if (this.started) {
                this.started = false;
                this.eventListeners.forEach((eventListener) => eventListener.disconnect());
            }
        }
        get eventListeners() {
            return Array.from(this.eventListenerMaps.values()).reduce((listeners, map) => listeners.concat(Array.from(map.values())), []);
        }
        bindingConnected(binding) {
            this.fetchEventListenerForBinding(binding).bindingConnected(binding);
        }
        bindingDisconnected(binding, clearEventListeners = false) {
            this.fetchEventListenerForBinding(binding).bindingDisconnected(binding);
            if (clearEventListeners)
                this.clearEventListenersForBinding(binding);
        }
        handleError(error, message, detail = {}) {
            this.application.handleError(error, `Error ${message}`, detail);
        }
        clearEventListenersForBinding(binding) {
            const eventListener = this.fetchEventListenerForBinding(binding);
            if (!eventListener.hasBindings()) {
                eventListener.disconnect();
                this.removeMappedEventListenerFor(binding);
            }
        }
        removeMappedEventListenerFor(binding) {
            const { eventTarget, eventName, eventOptions } = binding;
            const eventListenerMap = this.fetchEventListenerMapForEventTarget(eventTarget);
            const cacheKey = this.cacheKey(eventName, eventOptions);
            eventListenerMap.delete(cacheKey);
            if (eventListenerMap.size == 0)
                this.eventListenerMaps.delete(eventTarget);
        }
        fetchEventListenerForBinding(binding) {
            const { eventTarget, eventName, eventOptions } = binding;
            return this.fetchEventListener(eventTarget, eventName, eventOptions);
        }
        fetchEventListener(eventTarget, eventName, eventOptions) {
            const eventListenerMap = this.fetchEventListenerMapForEventTarget(eventTarget);
            const cacheKey = this.cacheKey(eventName, eventOptions);
            let eventListener = eventListenerMap.get(cacheKey);
            if (!eventListener) {
                eventListener = this.createEventListener(eventTarget, eventName, eventOptions);
                eventListenerMap.set(cacheKey, eventListener);
            }
            return eventListener;
        }
        createEventListener(eventTarget, eventName, eventOptions) {
            const eventListener = new EventListener(eventTarget, eventName, eventOptions);
            if (this.started) {
                eventListener.connect();
            }
            return eventListener;
        }
        fetchEventListenerMapForEventTarget(eventTarget) {
            let eventListenerMap = this.eventListenerMaps.get(eventTarget);
            if (!eventListenerMap) {
                eventListenerMap = new Map();
                this.eventListenerMaps.set(eventTarget, eventListenerMap);
            }
            return eventListenerMap;
        }
        cacheKey(eventName, eventOptions) {
            const parts = [eventName];
            Object.keys(eventOptions)
                .sort()
                .forEach((key) => {
                parts.push(`${eventOptions[key] ? "" : "!"}${key}`);
            });
            return parts.join(":");
        }
    }

    const defaultActionDescriptorFilters = {
        stop({ event, value }) {
            if (value)
                event.stopPropagation();
            return true;
        },
        prevent({ event, value }) {
            if (value)
                event.preventDefault();
            return true;
        },
        self({ event, value, element }) {
            if (value) {
                return element === event.target;
            }
            else {
                return true;
            }
        },
    };
    const descriptorPattern = /^(?:(?:([^.]+?)\+)?(.+?)(?:\.(.+?))?(?:@(window|document))?->)?(.+?)(?:#([^:]+?))(?::(.+))?$/;
    function parseActionDescriptorString(descriptorString) {
        const source = descriptorString.trim();
        const matches = source.match(descriptorPattern) || [];
        let eventName = matches[2];
        let keyFilter = matches[3];
        if (keyFilter && !["keydown", "keyup", "keypress"].includes(eventName)) {
            eventName += `.${keyFilter}`;
            keyFilter = "";
        }
        return {
            eventTarget: parseEventTarget(matches[4]),
            eventName,
            eventOptions: matches[7] ? parseEventOptions(matches[7]) : {},
            identifier: matches[5],
            methodName: matches[6],
            keyFilter: matches[1] || keyFilter,
        };
    }
    function parseEventTarget(eventTargetName) {
        if (eventTargetName == "window") {
            return window;
        }
        else if (eventTargetName == "document") {
            return document;
        }
    }
    function parseEventOptions(eventOptions) {
        return eventOptions
            .split(":")
            .reduce((options, token) => Object.assign(options, { [token.replace(/^!/, "")]: !/^!/.test(token) }), {});
    }
    function stringifyEventTarget(eventTarget) {
        if (eventTarget == window) {
            return "window";
        }
        else if (eventTarget == document) {
            return "document";
        }
    }

    function camelize(value) {
        return value.replace(/(?:[_-])([a-z0-9])/g, (_, char) => char.toUpperCase());
    }
    function namespaceCamelize(value) {
        return camelize(value.replace(/--/g, "-").replace(/__/g, "_"));
    }
    function capitalize(value) {
        return value.charAt(0).toUpperCase() + value.slice(1);
    }
    function dasherize(value) {
        return value.replace(/([A-Z])/g, (_, char) => `-${char.toLowerCase()}`);
    }
    function tokenize(value) {
        return value.match(/[^\s]+/g) || [];
    }

    function isSomething(object) {
        return object !== null && object !== undefined;
    }
    function hasProperty(object, property) {
        return Object.prototype.hasOwnProperty.call(object, property);
    }

    const allModifiers = ["meta", "ctrl", "alt", "shift"];
    class Action {
        constructor(element, index, descriptor, schema) {
            this.element = element;
            this.index = index;
            this.eventTarget = descriptor.eventTarget || element;
            this.eventName = descriptor.eventName || getDefaultEventNameForElement(element) || error("missing event name");
            this.eventOptions = descriptor.eventOptions || {};
            this.identifier = descriptor.identifier || error("missing identifier");
            this.methodName = descriptor.methodName || error("missing method name");
            this.keyFilter = descriptor.keyFilter || "";
            this.schema = schema;
        }
        static forToken(token, schema) {
            return new this(token.element, token.index, parseActionDescriptorString(token.content), schema);
        }
        toString() {
            const eventFilter = this.keyFilter ? `.${this.keyFilter}` : "";
            const eventTarget = this.eventTargetName ? `@${this.eventTargetName}` : "";
            return `${this.eventName}${eventFilter}${eventTarget}->${this.identifier}#${this.methodName}`;
        }
        shouldIgnoreKeyboardEvent(event) {
            if (!this.keyFilter) {
                return false;
            }
            const filters = this.keyFilter.split("+");
            if (this.keyFilterDissatisfied(event, filters)) {
                return true;
            }
            const standardFilter = filters.filter((key) => !allModifiers.includes(key))[0];
            if (!standardFilter) {
                return false;
            }
            if (!hasProperty(this.keyMappings, standardFilter)) {
                error(`contains unknown key filter: ${this.keyFilter}`);
            }
            return this.keyMappings[standardFilter].toLowerCase() !== event.key.toLowerCase();
        }
        shouldIgnoreMouseEvent(event) {
            if (!this.keyFilter) {
                return false;
            }
            const filters = [this.keyFilter];
            if (this.keyFilterDissatisfied(event, filters)) {
                return true;
            }
            return false;
        }
        get params() {
            const params = {};
            const pattern = new RegExp(`^data-${this.identifier}-(.+)-param$`, "i");
            for (const { name, value } of Array.from(this.element.attributes)) {
                const match = name.match(pattern);
                const key = match && match[1];
                if (key) {
                    params[camelize(key)] = typecast(value);
                }
            }
            return params;
        }
        get eventTargetName() {
            return stringifyEventTarget(this.eventTarget);
        }
        get keyMappings() {
            return this.schema.keyMappings;
        }
        keyFilterDissatisfied(event, filters) {
            const [meta, ctrl, alt, shift] = allModifiers.map((modifier) => filters.includes(modifier));
            return event.metaKey !== meta || event.ctrlKey !== ctrl || event.altKey !== alt || event.shiftKey !== shift;
        }
    }
    const defaultEventNames = {
        a: () => "click",
        button: () => "click",
        form: () => "submit",
        details: () => "toggle",
        input: (e) => (e.getAttribute("type") == "submit" ? "click" : "input"),
        select: () => "change",
        textarea: () => "input",
    };
    function getDefaultEventNameForElement(element) {
        const tagName = element.tagName.toLowerCase();
        if (tagName in defaultEventNames) {
            return defaultEventNames[tagName](element);
        }
    }
    function error(message) {
        throw new Error(message);
    }
    function typecast(value) {
        try {
            return JSON.parse(value);
        }
        catch (o_O) {
            return value;
        }
    }

    class Binding {
        constructor(context, action) {
            this.context = context;
            this.action = action;
        }
        get index() {
            return this.action.index;
        }
        get eventTarget() {
            return this.action.eventTarget;
        }
        get eventOptions() {
            return this.action.eventOptions;
        }
        get identifier() {
            return this.context.identifier;
        }
        handleEvent(event) {
            const actionEvent = this.prepareActionEvent(event);
            if (this.willBeInvokedByEvent(event) && this.applyEventModifiers(actionEvent)) {
                this.invokeWithEvent(actionEvent);
            }
        }
        get eventName() {
            return this.action.eventName;
        }
        get method() {
            const method = this.controller[this.methodName];
            if (typeof method == "function") {
                return method;
            }
            throw new Error(`Action "${this.action}" references undefined method "${this.methodName}"`);
        }
        applyEventModifiers(event) {
            const { element } = this.action;
            const { actionDescriptorFilters } = this.context.application;
            const { controller } = this.context;
            let passes = true;
            for (const [name, value] of Object.entries(this.eventOptions)) {
                if (name in actionDescriptorFilters) {
                    const filter = actionDescriptorFilters[name];
                    passes = passes && filter({ name, value, event, element, controller });
                }
                else {
                    continue;
                }
            }
            return passes;
        }
        prepareActionEvent(event) {
            return Object.assign(event, { params: this.action.params });
        }
        invokeWithEvent(event) {
            const { target, currentTarget } = event;
            try {
                this.method.call(this.controller, event);
                this.context.logDebugActivity(this.methodName, { event, target, currentTarget, action: this.methodName });
            }
            catch (error) {
                const { identifier, controller, element, index } = this;
                const detail = { identifier, controller, element, index, event };
                this.context.handleError(error, `invoking action "${this.action}"`, detail);
            }
        }
        willBeInvokedByEvent(event) {
            const eventTarget = event.target;
            if (event instanceof KeyboardEvent && this.action.shouldIgnoreKeyboardEvent(event)) {
                return false;
            }
            if (event instanceof MouseEvent && this.action.shouldIgnoreMouseEvent(event)) {
                return false;
            }
            if (this.element === eventTarget) {
                return true;
            }
            else if (eventTarget instanceof Element && this.element.contains(eventTarget)) {
                return this.scope.containsElement(eventTarget);
            }
            else {
                return this.scope.containsElement(this.action.element);
            }
        }
        get controller() {
            return this.context.controller;
        }
        get methodName() {
            return this.action.methodName;
        }
        get element() {
            return this.scope.element;
        }
        get scope() {
            return this.context.scope;
        }
    }

    class ElementObserver {
        constructor(element, delegate) {
            this.mutationObserverInit = { attributes: true, childList: true, subtree: true };
            this.element = element;
            this.started = false;
            this.delegate = delegate;
            this.elements = new Set();
            this.mutationObserver = new MutationObserver((mutations) => this.processMutations(mutations));
        }
        start() {
            if (!this.started) {
                this.started = true;
                this.mutationObserver.observe(this.element, this.mutationObserverInit);
                this.refresh();
            }
        }
        pause(callback) {
            if (this.started) {
                this.mutationObserver.disconnect();
                this.started = false;
            }
            callback();
            if (!this.started) {
                this.mutationObserver.observe(this.element, this.mutationObserverInit);
                this.started = true;
            }
        }
        stop() {
            if (this.started) {
                this.mutationObserver.takeRecords();
                this.mutationObserver.disconnect();
                this.started = false;
            }
        }
        refresh() {
            if (this.started) {
                const matches = new Set(this.matchElementsInTree());
                for (const element of Array.from(this.elements)) {
                    if (!matches.has(element)) {
                        this.removeElement(element);
                    }
                }
                for (const element of Array.from(matches)) {
                    this.addElement(element);
                }
            }
        }
        processMutations(mutations) {
            if (this.started) {
                for (const mutation of mutations) {
                    this.processMutation(mutation);
                }
            }
        }
        processMutation(mutation) {
            if (mutation.type == "attributes") {
                this.processAttributeChange(mutation.target, mutation.attributeName);
            }
            else if (mutation.type == "childList") {
                this.processRemovedNodes(mutation.removedNodes);
                this.processAddedNodes(mutation.addedNodes);
            }
        }
        processAttributeChange(element, attributeName) {
            if (this.elements.has(element)) {
                if (this.delegate.elementAttributeChanged && this.matchElement(element)) {
                    this.delegate.elementAttributeChanged(element, attributeName);
                }
                else {
                    this.removeElement(element);
                }
            }
            else if (this.matchElement(element)) {
                this.addElement(element);
            }
        }
        processRemovedNodes(nodes) {
            for (const node of Array.from(nodes)) {
                const element = this.elementFromNode(node);
                if (element) {
                    this.processTree(element, this.removeElement);
                }
            }
        }
        processAddedNodes(nodes) {
            for (const node of Array.from(nodes)) {
                const element = this.elementFromNode(node);
                if (element && this.elementIsActive(element)) {
                    this.processTree(element, this.addElement);
                }
            }
        }
        matchElement(element) {
            return this.delegate.matchElement(element);
        }
        matchElementsInTree(tree = this.element) {
            return this.delegate.matchElementsInTree(tree);
        }
        processTree(tree, processor) {
            for (const element of this.matchElementsInTree(tree)) {
                processor.call(this, element);
            }
        }
        elementFromNode(node) {
            if (node.nodeType == Node.ELEMENT_NODE) {
                return node;
            }
        }
        elementIsActive(element) {
            if (element.isConnected != this.element.isConnected) {
                return false;
            }
            else {
                return this.element.contains(element);
            }
        }
        addElement(element) {
            if (!this.elements.has(element)) {
                if (this.elementIsActive(element)) {
                    this.elements.add(element);
                    if (this.delegate.elementMatched) {
                        this.delegate.elementMatched(element);
                    }
                }
            }
        }
        removeElement(element) {
            if (this.elements.has(element)) {
                this.elements.delete(element);
                if (this.delegate.elementUnmatched) {
                    this.delegate.elementUnmatched(element);
                }
            }
        }
    }

    class AttributeObserver {
        constructor(element, attributeName, delegate) {
            this.attributeName = attributeName;
            this.delegate = delegate;
            this.elementObserver = new ElementObserver(element, this);
        }
        get element() {
            return this.elementObserver.element;
        }
        get selector() {
            return `[${this.attributeName}]`;
        }
        start() {
            this.elementObserver.start();
        }
        pause(callback) {
            this.elementObserver.pause(callback);
        }
        stop() {
            this.elementObserver.stop();
        }
        refresh() {
            this.elementObserver.refresh();
        }
        get started() {
            return this.elementObserver.started;
        }
        matchElement(element) {
            return element.hasAttribute(this.attributeName);
        }
        matchElementsInTree(tree) {
            const match = this.matchElement(tree) ? [tree] : [];
            const matches = Array.from(tree.querySelectorAll(this.selector));
            return match.concat(matches);
        }
        elementMatched(element) {
            if (this.delegate.elementMatchedAttribute) {
                this.delegate.elementMatchedAttribute(element, this.attributeName);
            }
        }
        elementUnmatched(element) {
            if (this.delegate.elementUnmatchedAttribute) {
                this.delegate.elementUnmatchedAttribute(element, this.attributeName);
            }
        }
        elementAttributeChanged(element, attributeName) {
            if (this.delegate.elementAttributeValueChanged && this.attributeName == attributeName) {
                this.delegate.elementAttributeValueChanged(element, attributeName);
            }
        }
    }

    function add(map, key, value) {
        fetch(map, key).add(value);
    }
    function del(map, key, value) {
        fetch(map, key).delete(value);
        prune(map, key);
    }
    function fetch(map, key) {
        let values = map.get(key);
        if (!values) {
            values = new Set();
            map.set(key, values);
        }
        return values;
    }
    function prune(map, key) {
        const values = map.get(key);
        if (values != null && values.size == 0) {
            map.delete(key);
        }
    }

    class Multimap {
        constructor() {
            this.valuesByKey = new Map();
        }
        get keys() {
            return Array.from(this.valuesByKey.keys());
        }
        get values() {
            const sets = Array.from(this.valuesByKey.values());
            return sets.reduce((values, set) => values.concat(Array.from(set)), []);
        }
        get size() {
            const sets = Array.from(this.valuesByKey.values());
            return sets.reduce((size, set) => size + set.size, 0);
        }
        add(key, value) {
            add(this.valuesByKey, key, value);
        }
        delete(key, value) {
            del(this.valuesByKey, key, value);
        }
        has(key, value) {
            const values = this.valuesByKey.get(key);
            return values != null && values.has(value);
        }
        hasKey(key) {
            return this.valuesByKey.has(key);
        }
        hasValue(value) {
            const sets = Array.from(this.valuesByKey.values());
            return sets.some((set) => set.has(value));
        }
        getValuesForKey(key) {
            const values = this.valuesByKey.get(key);
            return values ? Array.from(values) : [];
        }
        getKeysForValue(value) {
            return Array.from(this.valuesByKey)
                .filter(([_key, values]) => values.has(value))
                .map(([key, _values]) => key);
        }
    }

    class IndexedMultimap extends Multimap {
        constructor() {
            super();
            this.keysByValue = new Map();
        }
        get values() {
            return Array.from(this.keysByValue.keys());
        }
        add(key, value) {
            super.add(key, value);
            add(this.keysByValue, value, key);
        }
        delete(key, value) {
            super.delete(key, value);
            del(this.keysByValue, value, key);
        }
        hasValue(value) {
            return this.keysByValue.has(value);
        }
        getKeysForValue(value) {
            const set = this.keysByValue.get(value);
            return set ? Array.from(set) : [];
        }
    }

    class SelectorObserver {
        constructor(element, selector, delegate, details) {
            this._selector = selector;
            this.details = details;
            this.elementObserver = new ElementObserver(element, this);
            this.delegate = delegate;
            this.matchesByElement = new Multimap();
        }
        get started() {
            return this.elementObserver.started;
        }
        get selector() {
            return this._selector;
        }
        set selector(selector) {
            this._selector = selector;
            this.refresh();
        }
        start() {
            this.elementObserver.start();
        }
        pause(callback) {
            this.elementObserver.pause(callback);
        }
        stop() {
            this.elementObserver.stop();
        }
        refresh() {
            this.elementObserver.refresh();
        }
        get element() {
            return this.elementObserver.element;
        }
        matchElement(element) {
            const { selector } = this;
            if (selector) {
                const matches = element.matches(selector);
                if (this.delegate.selectorMatchElement) {
                    return matches && this.delegate.selectorMatchElement(element, this.details);
                }
                return matches;
            }
            else {
                return false;
            }
        }
        matchElementsInTree(tree) {
            const { selector } = this;
            if (selector) {
                const match = this.matchElement(tree) ? [tree] : [];
                const matches = Array.from(tree.querySelectorAll(selector)).filter((match) => this.matchElement(match));
                return match.concat(matches);
            }
            else {
                return [];
            }
        }
        elementMatched(element) {
            const { selector } = this;
            if (selector) {
                this.selectorMatched(element, selector);
            }
        }
        elementUnmatched(element) {
            const selectors = this.matchesByElement.getKeysForValue(element);
            for (const selector of selectors) {
                this.selectorUnmatched(element, selector);
            }
        }
        elementAttributeChanged(element, _attributeName) {
            const { selector } = this;
            if (selector) {
                const matches = this.matchElement(element);
                const matchedBefore = this.matchesByElement.has(selector, element);
                if (matches && !matchedBefore) {
                    this.selectorMatched(element, selector);
                }
                else if (!matches && matchedBefore) {
                    this.selectorUnmatched(element, selector);
                }
            }
        }
        selectorMatched(element, selector) {
            this.delegate.selectorMatched(element, selector, this.details);
            this.matchesByElement.add(selector, element);
        }
        selectorUnmatched(element, selector) {
            this.delegate.selectorUnmatched(element, selector, this.details);
            this.matchesByElement.delete(selector, element);
        }
    }

    class StringMapObserver {
        constructor(element, delegate) {
            this.element = element;
            this.delegate = delegate;
            this.started = false;
            this.stringMap = new Map();
            this.mutationObserver = new MutationObserver((mutations) => this.processMutations(mutations));
        }
        start() {
            if (!this.started) {
                this.started = true;
                this.mutationObserver.observe(this.element, { attributes: true, attributeOldValue: true });
                this.refresh();
            }
        }
        stop() {
            if (this.started) {
                this.mutationObserver.takeRecords();
                this.mutationObserver.disconnect();
                this.started = false;
            }
        }
        refresh() {
            if (this.started) {
                for (const attributeName of this.knownAttributeNames) {
                    this.refreshAttribute(attributeName, null);
                }
            }
        }
        processMutations(mutations) {
            if (this.started) {
                for (const mutation of mutations) {
                    this.processMutation(mutation);
                }
            }
        }
        processMutation(mutation) {
            const attributeName = mutation.attributeName;
            if (attributeName) {
                this.refreshAttribute(attributeName, mutation.oldValue);
            }
        }
        refreshAttribute(attributeName, oldValue) {
            const key = this.delegate.getStringMapKeyForAttribute(attributeName);
            if (key != null) {
                if (!this.stringMap.has(attributeName)) {
                    this.stringMapKeyAdded(key, attributeName);
                }
                const value = this.element.getAttribute(attributeName);
                if (this.stringMap.get(attributeName) != value) {
                    this.stringMapValueChanged(value, key, oldValue);
                }
                if (value == null) {
                    const oldValue = this.stringMap.get(attributeName);
                    this.stringMap.delete(attributeName);
                    if (oldValue)
                        this.stringMapKeyRemoved(key, attributeName, oldValue);
                }
                else {
                    this.stringMap.set(attributeName, value);
                }
            }
        }
        stringMapKeyAdded(key, attributeName) {
            if (this.delegate.stringMapKeyAdded) {
                this.delegate.stringMapKeyAdded(key, attributeName);
            }
        }
        stringMapValueChanged(value, key, oldValue) {
            if (this.delegate.stringMapValueChanged) {
                this.delegate.stringMapValueChanged(value, key, oldValue);
            }
        }
        stringMapKeyRemoved(key, attributeName, oldValue) {
            if (this.delegate.stringMapKeyRemoved) {
                this.delegate.stringMapKeyRemoved(key, attributeName, oldValue);
            }
        }
        get knownAttributeNames() {
            return Array.from(new Set(this.currentAttributeNames.concat(this.recordedAttributeNames)));
        }
        get currentAttributeNames() {
            return Array.from(this.element.attributes).map((attribute) => attribute.name);
        }
        get recordedAttributeNames() {
            return Array.from(this.stringMap.keys());
        }
    }

    class TokenListObserver {
        constructor(element, attributeName, delegate) {
            this.attributeObserver = new AttributeObserver(element, attributeName, this);
            this.delegate = delegate;
            this.tokensByElement = new Multimap();
        }
        get started() {
            return this.attributeObserver.started;
        }
        start() {
            this.attributeObserver.start();
        }
        pause(callback) {
            this.attributeObserver.pause(callback);
        }
        stop() {
            this.attributeObserver.stop();
        }
        refresh() {
            this.attributeObserver.refresh();
        }
        get element() {
            return this.attributeObserver.element;
        }
        get attributeName() {
            return this.attributeObserver.attributeName;
        }
        elementMatchedAttribute(element) {
            this.tokensMatched(this.readTokensForElement(element));
        }
        elementAttributeValueChanged(element) {
            const [unmatchedTokens, matchedTokens] = this.refreshTokensForElement(element);
            this.tokensUnmatched(unmatchedTokens);
            this.tokensMatched(matchedTokens);
        }
        elementUnmatchedAttribute(element) {
            this.tokensUnmatched(this.tokensByElement.getValuesForKey(element));
        }
        tokensMatched(tokens) {
            tokens.forEach((token) => this.tokenMatched(token));
        }
        tokensUnmatched(tokens) {
            tokens.forEach((token) => this.tokenUnmatched(token));
        }
        tokenMatched(token) {
            this.delegate.tokenMatched(token);
            this.tokensByElement.add(token.element, token);
        }
        tokenUnmatched(token) {
            this.delegate.tokenUnmatched(token);
            this.tokensByElement.delete(token.element, token);
        }
        refreshTokensForElement(element) {
            const previousTokens = this.tokensByElement.getValuesForKey(element);
            const currentTokens = this.readTokensForElement(element);
            const firstDifferingIndex = zip(previousTokens, currentTokens).findIndex(([previousToken, currentToken]) => !tokensAreEqual(previousToken, currentToken));
            if (firstDifferingIndex == -1) {
                return [[], []];
            }
            else {
                return [previousTokens.slice(firstDifferingIndex), currentTokens.slice(firstDifferingIndex)];
            }
        }
        readTokensForElement(element) {
            const attributeName = this.attributeName;
            const tokenString = element.getAttribute(attributeName) || "";
            return parseTokenString(tokenString, element, attributeName);
        }
    }
    function parseTokenString(tokenString, element, attributeName) {
        return tokenString
            .trim()
            .split(/\s+/)
            .filter((content) => content.length)
            .map((content, index) => ({ element, attributeName, content, index }));
    }
    function zip(left, right) {
        const length = Math.max(left.length, right.length);
        return Array.from({ length }, (_, index) => [left[index], right[index]]);
    }
    function tokensAreEqual(left, right) {
        return left && right && left.index == right.index && left.content == right.content;
    }

    class ValueListObserver {
        constructor(element, attributeName, delegate) {
            this.tokenListObserver = new TokenListObserver(element, attributeName, this);
            this.delegate = delegate;
            this.parseResultsByToken = new WeakMap();
            this.valuesByTokenByElement = new WeakMap();
        }
        get started() {
            return this.tokenListObserver.started;
        }
        start() {
            this.tokenListObserver.start();
        }
        stop() {
            this.tokenListObserver.stop();
        }
        refresh() {
            this.tokenListObserver.refresh();
        }
        get element() {
            return this.tokenListObserver.element;
        }
        get attributeName() {
            return this.tokenListObserver.attributeName;
        }
        tokenMatched(token) {
            const { element } = token;
            const { value } = this.fetchParseResultForToken(token);
            if (value) {
                this.fetchValuesByTokenForElement(element).set(token, value);
                this.delegate.elementMatchedValue(element, value);
            }
        }
        tokenUnmatched(token) {
            const { element } = token;
            const { value } = this.fetchParseResultForToken(token);
            if (value) {
                this.fetchValuesByTokenForElement(element).delete(token);
                this.delegate.elementUnmatchedValue(element, value);
            }
        }
        fetchParseResultForToken(token) {
            let parseResult = this.parseResultsByToken.get(token);
            if (!parseResult) {
                parseResult = this.parseToken(token);
                this.parseResultsByToken.set(token, parseResult);
            }
            return parseResult;
        }
        fetchValuesByTokenForElement(element) {
            let valuesByToken = this.valuesByTokenByElement.get(element);
            if (!valuesByToken) {
                valuesByToken = new Map();
                this.valuesByTokenByElement.set(element, valuesByToken);
            }
            return valuesByToken;
        }
        parseToken(token) {
            try {
                const value = this.delegate.parseValueForToken(token);
                return { value };
            }
            catch (error) {
                return { error };
            }
        }
    }

    class BindingObserver {
        constructor(context, delegate) {
            this.context = context;
            this.delegate = delegate;
            this.bindingsByAction = new Map();
        }
        start() {
            if (!this.valueListObserver) {
                this.valueListObserver = new ValueListObserver(this.element, this.actionAttribute, this);
                this.valueListObserver.start();
            }
        }
        stop() {
            if (this.valueListObserver) {
                this.valueListObserver.stop();
                delete this.valueListObserver;
                this.disconnectAllActions();
            }
        }
        get element() {
            return this.context.element;
        }
        get identifier() {
            return this.context.identifier;
        }
        get actionAttribute() {
            return this.schema.actionAttribute;
        }
        get schema() {
            return this.context.schema;
        }
        get bindings() {
            return Array.from(this.bindingsByAction.values());
        }
        connectAction(action) {
            const binding = new Binding(this.context, action);
            this.bindingsByAction.set(action, binding);
            this.delegate.bindingConnected(binding);
        }
        disconnectAction(action) {
            const binding = this.bindingsByAction.get(action);
            if (binding) {
                this.bindingsByAction.delete(action);
                this.delegate.bindingDisconnected(binding);
            }
        }
        disconnectAllActions() {
            this.bindings.forEach((binding) => this.delegate.bindingDisconnected(binding, true));
            this.bindingsByAction.clear();
        }
        parseValueForToken(token) {
            const action = Action.forToken(token, this.schema);
            if (action.identifier == this.identifier) {
                return action;
            }
        }
        elementMatchedValue(element, action) {
            this.connectAction(action);
        }
        elementUnmatchedValue(element, action) {
            this.disconnectAction(action);
        }
    }

    class ValueObserver {
        constructor(context, receiver) {
            this.context = context;
            this.receiver = receiver;
            this.stringMapObserver = new StringMapObserver(this.element, this);
            this.valueDescriptorMap = this.controller.valueDescriptorMap;
        }
        start() {
            this.stringMapObserver.start();
            this.invokeChangedCallbacksForDefaultValues();
        }
        stop() {
            this.stringMapObserver.stop();
        }
        get element() {
            return this.context.element;
        }
        get controller() {
            return this.context.controller;
        }
        getStringMapKeyForAttribute(attributeName) {
            if (attributeName in this.valueDescriptorMap) {
                return this.valueDescriptorMap[attributeName].name;
            }
        }
        stringMapKeyAdded(key, attributeName) {
            const descriptor = this.valueDescriptorMap[attributeName];
            if (!this.hasValue(key)) {
                this.invokeChangedCallback(key, descriptor.writer(this.receiver[key]), descriptor.writer(descriptor.defaultValue));
            }
        }
        stringMapValueChanged(value, name, oldValue) {
            const descriptor = this.valueDescriptorNameMap[name];
            if (value === null)
                return;
            if (oldValue === null) {
                oldValue = descriptor.writer(descriptor.defaultValue);
            }
            this.invokeChangedCallback(name, value, oldValue);
        }
        stringMapKeyRemoved(key, attributeName, oldValue) {
            const descriptor = this.valueDescriptorNameMap[key];
            if (this.hasValue(key)) {
                this.invokeChangedCallback(key, descriptor.writer(this.receiver[key]), oldValue);
            }
            else {
                this.invokeChangedCallback(key, descriptor.writer(descriptor.defaultValue), oldValue);
            }
        }
        invokeChangedCallbacksForDefaultValues() {
            for (const { key, name, defaultValue, writer } of this.valueDescriptors) {
                if (defaultValue != undefined && !this.controller.data.has(key)) {
                    this.invokeChangedCallback(name, writer(defaultValue), undefined);
                }
            }
        }
        invokeChangedCallback(name, rawValue, rawOldValue) {
            const changedMethodName = `${name}Changed`;
            const changedMethod = this.receiver[changedMethodName];
            if (typeof changedMethod == "function") {
                const descriptor = this.valueDescriptorNameMap[name];
                try {
                    const value = descriptor.reader(rawValue);
                    let oldValue = rawOldValue;
                    if (rawOldValue) {
                        oldValue = descriptor.reader(rawOldValue);
                    }
                    changedMethod.call(this.receiver, value, oldValue);
                }
                catch (error) {
                    if (error instanceof TypeError) {
                        error.message = `Stimulus Value "${this.context.identifier}.${descriptor.name}" - ${error.message}`;
                    }
                    throw error;
                }
            }
        }
        get valueDescriptors() {
            const { valueDescriptorMap } = this;
            return Object.keys(valueDescriptorMap).map((key) => valueDescriptorMap[key]);
        }
        get valueDescriptorNameMap() {
            const descriptors = {};
            Object.keys(this.valueDescriptorMap).forEach((key) => {
                const descriptor = this.valueDescriptorMap[key];
                descriptors[descriptor.name] = descriptor;
            });
            return descriptors;
        }
        hasValue(attributeName) {
            const descriptor = this.valueDescriptorNameMap[attributeName];
            const hasMethodName = `has${capitalize(descriptor.name)}`;
            return this.receiver[hasMethodName];
        }
    }

    class TargetObserver {
        constructor(context, delegate) {
            this.context = context;
            this.delegate = delegate;
            this.targetsByName = new Multimap();
        }
        start() {
            if (!this.tokenListObserver) {
                this.tokenListObserver = new TokenListObserver(this.element, this.attributeName, this);
                this.tokenListObserver.start();
            }
        }
        stop() {
            if (this.tokenListObserver) {
                this.disconnectAllTargets();
                this.tokenListObserver.stop();
                delete this.tokenListObserver;
            }
        }
        tokenMatched({ element, content: name }) {
            if (this.scope.containsElement(element)) {
                this.connectTarget(element, name);
            }
        }
        tokenUnmatched({ element, content: name }) {
            this.disconnectTarget(element, name);
        }
        connectTarget(element, name) {
            var _a;
            if (!this.targetsByName.has(name, element)) {
                this.targetsByName.add(name, element);
                (_a = this.tokenListObserver) === null || _a === void 0 ? void 0 : _a.pause(() => this.delegate.targetConnected(element, name));
            }
        }
        disconnectTarget(element, name) {
            var _a;
            if (this.targetsByName.has(name, element)) {
                this.targetsByName.delete(name, element);
                (_a = this.tokenListObserver) === null || _a === void 0 ? void 0 : _a.pause(() => this.delegate.targetDisconnected(element, name));
            }
        }
        disconnectAllTargets() {
            for (const name of this.targetsByName.keys) {
                for (const element of this.targetsByName.getValuesForKey(name)) {
                    this.disconnectTarget(element, name);
                }
            }
        }
        get attributeName() {
            return `data-${this.context.identifier}-target`;
        }
        get element() {
            return this.context.element;
        }
        get scope() {
            return this.context.scope;
        }
    }

    function readInheritableStaticArrayValues(constructor, propertyName) {
        const ancestors = getAncestorsForConstructor(constructor);
        return Array.from(ancestors.reduce((values, constructor) => {
            getOwnStaticArrayValues(constructor, propertyName).forEach((name) => values.add(name));
            return values;
        }, new Set()));
    }
    function readInheritableStaticObjectPairs(constructor, propertyName) {
        const ancestors = getAncestorsForConstructor(constructor);
        return ancestors.reduce((pairs, constructor) => {
            pairs.push(...getOwnStaticObjectPairs(constructor, propertyName));
            return pairs;
        }, []);
    }
    function getAncestorsForConstructor(constructor) {
        const ancestors = [];
        while (constructor) {
            ancestors.push(constructor);
            constructor = Object.getPrototypeOf(constructor);
        }
        return ancestors.reverse();
    }
    function getOwnStaticArrayValues(constructor, propertyName) {
        const definition = constructor[propertyName];
        return Array.isArray(definition) ? definition : [];
    }
    function getOwnStaticObjectPairs(constructor, propertyName) {
        const definition = constructor[propertyName];
        return definition ? Object.keys(definition).map((key) => [key, definition[key]]) : [];
    }

    class OutletObserver {
        constructor(context, delegate) {
            this.started = false;
            this.context = context;
            this.delegate = delegate;
            this.outletsByName = new Multimap();
            this.outletElementsByName = new Multimap();
            this.selectorObserverMap = new Map();
            this.attributeObserverMap = new Map();
        }
        start() {
            if (!this.started) {
                this.outletDefinitions.forEach((outletName) => {
                    this.setupSelectorObserverForOutlet(outletName);
                    this.setupAttributeObserverForOutlet(outletName);
                });
                this.started = true;
                this.dependentContexts.forEach((context) => context.refresh());
            }
        }
        refresh() {
            this.selectorObserverMap.forEach((observer) => observer.refresh());
            this.attributeObserverMap.forEach((observer) => observer.refresh());
        }
        stop() {
            if (this.started) {
                this.started = false;
                this.disconnectAllOutlets();
                this.stopSelectorObservers();
                this.stopAttributeObservers();
            }
        }
        stopSelectorObservers() {
            if (this.selectorObserverMap.size > 0) {
                this.selectorObserverMap.forEach((observer) => observer.stop());
                this.selectorObserverMap.clear();
            }
        }
        stopAttributeObservers() {
            if (this.attributeObserverMap.size > 0) {
                this.attributeObserverMap.forEach((observer) => observer.stop());
                this.attributeObserverMap.clear();
            }
        }
        selectorMatched(element, _selector, { outletName }) {
            const outlet = this.getOutlet(element, outletName);
            if (outlet) {
                this.connectOutlet(outlet, element, outletName);
            }
        }
        selectorUnmatched(element, _selector, { outletName }) {
            const outlet = this.getOutletFromMap(element, outletName);
            if (outlet) {
                this.disconnectOutlet(outlet, element, outletName);
            }
        }
        selectorMatchElement(element, { outletName }) {
            const selector = this.selector(outletName);
            const hasOutlet = this.hasOutlet(element, outletName);
            const hasOutletController = element.matches(`[${this.schema.controllerAttribute}~=${outletName}]`);
            if (selector) {
                return hasOutlet && hasOutletController && element.matches(selector);
            }
            else {
                return false;
            }
        }
        elementMatchedAttribute(_element, attributeName) {
            const outletName = this.getOutletNameFromOutletAttributeName(attributeName);
            if (outletName) {
                this.updateSelectorObserverForOutlet(outletName);
            }
        }
        elementAttributeValueChanged(_element, attributeName) {
            const outletName = this.getOutletNameFromOutletAttributeName(attributeName);
            if (outletName) {
                this.updateSelectorObserverForOutlet(outletName);
            }
        }
        elementUnmatchedAttribute(_element, attributeName) {
            const outletName = this.getOutletNameFromOutletAttributeName(attributeName);
            if (outletName) {
                this.updateSelectorObserverForOutlet(outletName);
            }
        }
        connectOutlet(outlet, element, outletName) {
            var _a;
            if (!this.outletElementsByName.has(outletName, element)) {
                this.outletsByName.add(outletName, outlet);
                this.outletElementsByName.add(outletName, element);
                (_a = this.selectorObserverMap.get(outletName)) === null || _a === void 0 ? void 0 : _a.pause(() => this.delegate.outletConnected(outlet, element, outletName));
            }
        }
        disconnectOutlet(outlet, element, outletName) {
            var _a;
            if (this.outletElementsByName.has(outletName, element)) {
                this.outletsByName.delete(outletName, outlet);
                this.outletElementsByName.delete(outletName, element);
                (_a = this.selectorObserverMap
                    .get(outletName)) === null || _a === void 0 ? void 0 : _a.pause(() => this.delegate.outletDisconnected(outlet, element, outletName));
            }
        }
        disconnectAllOutlets() {
            for (const outletName of this.outletElementsByName.keys) {
                for (const element of this.outletElementsByName.getValuesForKey(outletName)) {
                    for (const outlet of this.outletsByName.getValuesForKey(outletName)) {
                        this.disconnectOutlet(outlet, element, outletName);
                    }
                }
            }
        }
        updateSelectorObserverForOutlet(outletName) {
            const observer = this.selectorObserverMap.get(outletName);
            if (observer) {
                observer.selector = this.selector(outletName);
            }
        }
        setupSelectorObserverForOutlet(outletName) {
            const selector = this.selector(outletName);
            const selectorObserver = new SelectorObserver(document.body, selector, this, { outletName });
            this.selectorObserverMap.set(outletName, selectorObserver);
            selectorObserver.start();
        }
        setupAttributeObserverForOutlet(outletName) {
            const attributeName = this.attributeNameForOutletName(outletName);
            const attributeObserver = new AttributeObserver(this.scope.element, attributeName, this);
            this.attributeObserverMap.set(outletName, attributeObserver);
            attributeObserver.start();
        }
        selector(outletName) {
            return this.scope.outlets.getSelectorForOutletName(outletName);
        }
        attributeNameForOutletName(outletName) {
            return this.scope.schema.outletAttributeForScope(this.identifier, outletName);
        }
        getOutletNameFromOutletAttributeName(attributeName) {
            return this.outletDefinitions.find((outletName) => this.attributeNameForOutletName(outletName) === attributeName);
        }
        get outletDependencies() {
            const dependencies = new Multimap();
            this.router.modules.forEach((module) => {
                const constructor = module.definition.controllerConstructor;
                const outlets = readInheritableStaticArrayValues(constructor, "outlets");
                outlets.forEach((outlet) => dependencies.add(outlet, module.identifier));
            });
            return dependencies;
        }
        get outletDefinitions() {
            return this.outletDependencies.getKeysForValue(this.identifier);
        }
        get dependentControllerIdentifiers() {
            return this.outletDependencies.getValuesForKey(this.identifier);
        }
        get dependentContexts() {
            const identifiers = this.dependentControllerIdentifiers;
            return this.router.contexts.filter((context) => identifiers.includes(context.identifier));
        }
        hasOutlet(element, outletName) {
            return !!this.getOutlet(element, outletName) || !!this.getOutletFromMap(element, outletName);
        }
        getOutlet(element, outletName) {
            return this.application.getControllerForElementAndIdentifier(element, outletName);
        }
        getOutletFromMap(element, outletName) {
            return this.outletsByName.getValuesForKey(outletName).find((outlet) => outlet.element === element);
        }
        get scope() {
            return this.context.scope;
        }
        get schema() {
            return this.context.schema;
        }
        get identifier() {
            return this.context.identifier;
        }
        get application() {
            return this.context.application;
        }
        get router() {
            return this.application.router;
        }
    }

    class Context {
        constructor(module, scope) {
            this.logDebugActivity = (functionName, detail = {}) => {
                const { identifier, controller, element } = this;
                detail = Object.assign({ identifier, controller, element }, detail);
                this.application.logDebugActivity(this.identifier, functionName, detail);
            };
            this.module = module;
            this.scope = scope;
            this.controller = new module.controllerConstructor(this);
            this.bindingObserver = new BindingObserver(this, this.dispatcher);
            this.valueObserver = new ValueObserver(this, this.controller);
            this.targetObserver = new TargetObserver(this, this);
            this.outletObserver = new OutletObserver(this, this);
            try {
                this.controller.initialize();
                this.logDebugActivity("initialize");
            }
            catch (error) {
                this.handleError(error, "initializing controller");
            }
        }
        connect() {
            this.bindingObserver.start();
            this.valueObserver.start();
            this.targetObserver.start();
            this.outletObserver.start();
            try {
                this.controller.connect();
                this.logDebugActivity("connect");
            }
            catch (error) {
                this.handleError(error, "connecting controller");
            }
        }
        refresh() {
            this.outletObserver.refresh();
        }
        disconnect() {
            try {
                this.controller.disconnect();
                this.logDebugActivity("disconnect");
            }
            catch (error) {
                this.handleError(error, "disconnecting controller");
            }
            this.outletObserver.stop();
            this.targetObserver.stop();
            this.valueObserver.stop();
            this.bindingObserver.stop();
        }
        get application() {
            return this.module.application;
        }
        get identifier() {
            return this.module.identifier;
        }
        get schema() {
            return this.application.schema;
        }
        get dispatcher() {
            return this.application.dispatcher;
        }
        get element() {
            return this.scope.element;
        }
        get parentElement() {
            return this.element.parentElement;
        }
        handleError(error, message, detail = {}) {
            const { identifier, controller, element } = this;
            detail = Object.assign({ identifier, controller, element }, detail);
            this.application.handleError(error, `Error ${message}`, detail);
        }
        targetConnected(element, name) {
            this.invokeControllerMethod(`${name}TargetConnected`, element);
        }
        targetDisconnected(element, name) {
            this.invokeControllerMethod(`${name}TargetDisconnected`, element);
        }
        outletConnected(outlet, element, name) {
            this.invokeControllerMethod(`${namespaceCamelize(name)}OutletConnected`, outlet, element);
        }
        outletDisconnected(outlet, element, name) {
            this.invokeControllerMethod(`${namespaceCamelize(name)}OutletDisconnected`, outlet, element);
        }
        invokeControllerMethod(methodName, ...args) {
            const controller = this.controller;
            if (typeof controller[methodName] == "function") {
                controller[methodName](...args);
            }
        }
    }

    function bless(constructor) {
        return shadow(constructor, getBlessedProperties(constructor));
    }
    function shadow(constructor, properties) {
        const shadowConstructor = extend(constructor);
        const shadowProperties = getShadowProperties(constructor.prototype, properties);
        Object.defineProperties(shadowConstructor.prototype, shadowProperties);
        return shadowConstructor;
    }
    function getBlessedProperties(constructor) {
        const blessings = readInheritableStaticArrayValues(constructor, "blessings");
        return blessings.reduce((blessedProperties, blessing) => {
            const properties = blessing(constructor);
            for (const key in properties) {
                const descriptor = blessedProperties[key] || {};
                blessedProperties[key] = Object.assign(descriptor, properties[key]);
            }
            return blessedProperties;
        }, {});
    }
    function getShadowProperties(prototype, properties) {
        return getOwnKeys(properties).reduce((shadowProperties, key) => {
            const descriptor = getShadowedDescriptor(prototype, properties, key);
            if (descriptor) {
                Object.assign(shadowProperties, { [key]: descriptor });
            }
            return shadowProperties;
        }, {});
    }
    function getShadowedDescriptor(prototype, properties, key) {
        const shadowingDescriptor = Object.getOwnPropertyDescriptor(prototype, key);
        const shadowedByValue = shadowingDescriptor && "value" in shadowingDescriptor;
        if (!shadowedByValue) {
            const descriptor = Object.getOwnPropertyDescriptor(properties, key).value;
            if (shadowingDescriptor) {
                descriptor.get = shadowingDescriptor.get || descriptor.get;
                descriptor.set = shadowingDescriptor.set || descriptor.set;
            }
            return descriptor;
        }
    }
    const getOwnKeys = (() => {
        if (typeof Object.getOwnPropertySymbols == "function") {
            return (object) => [...Object.getOwnPropertyNames(object), ...Object.getOwnPropertySymbols(object)];
        }
        else {
            return Object.getOwnPropertyNames;
        }
    })();
    const extend = (() => {
        function extendWithReflect(constructor) {
            function extended() {
                return Reflect.construct(constructor, arguments, new.target);
            }
            extended.prototype = Object.create(constructor.prototype, {
                constructor: { value: extended },
            });
            Reflect.setPrototypeOf(extended, constructor);
            return extended;
        }
        function testReflectExtension() {
            const a = function () {
                this.a.call(this);
            };
            const b = extendWithReflect(a);
            b.prototype.a = function () { };
            return new b();
        }
        try {
            testReflectExtension();
            return extendWithReflect;
        }
        catch (error) {
            return (constructor) => class extended extends constructor {
            };
        }
    })();

    function blessDefinition(definition) {
        return {
            identifier: definition.identifier,
            controllerConstructor: bless(definition.controllerConstructor),
        };
    }

    class Module {
        constructor(application, definition) {
            this.application = application;
            this.definition = blessDefinition(definition);
            this.contextsByScope = new WeakMap();
            this.connectedContexts = new Set();
        }
        get identifier() {
            return this.definition.identifier;
        }
        get controllerConstructor() {
            return this.definition.controllerConstructor;
        }
        get contexts() {
            return Array.from(this.connectedContexts);
        }
        connectContextForScope(scope) {
            const context = this.fetchContextForScope(scope);
            this.connectedContexts.add(context);
            context.connect();
        }
        disconnectContextForScope(scope) {
            const context = this.contextsByScope.get(scope);
            if (context) {
                this.connectedContexts.delete(context);
                context.disconnect();
            }
        }
        fetchContextForScope(scope) {
            let context = this.contextsByScope.get(scope);
            if (!context) {
                context = new Context(this, scope);
                this.contextsByScope.set(scope, context);
            }
            return context;
        }
    }

    class ClassMap {
        constructor(scope) {
            this.scope = scope;
        }
        has(name) {
            return this.data.has(this.getDataKey(name));
        }
        get(name) {
            return this.getAll(name)[0];
        }
        getAll(name) {
            const tokenString = this.data.get(this.getDataKey(name)) || "";
            return tokenize(tokenString);
        }
        getAttributeName(name) {
            return this.data.getAttributeNameForKey(this.getDataKey(name));
        }
        getDataKey(name) {
            return `${name}-class`;
        }
        get data() {
            return this.scope.data;
        }
    }

    class DataMap {
        constructor(scope) {
            this.scope = scope;
        }
        get element() {
            return this.scope.element;
        }
        get identifier() {
            return this.scope.identifier;
        }
        get(key) {
            const name = this.getAttributeNameForKey(key);
            return this.element.getAttribute(name);
        }
        set(key, value) {
            const name = this.getAttributeNameForKey(key);
            this.element.setAttribute(name, value);
            return this.get(key);
        }
        has(key) {
            const name = this.getAttributeNameForKey(key);
            return this.element.hasAttribute(name);
        }
        delete(key) {
            if (this.has(key)) {
                const name = this.getAttributeNameForKey(key);
                this.element.removeAttribute(name);
                return true;
            }
            else {
                return false;
            }
        }
        getAttributeNameForKey(key) {
            return `data-${this.identifier}-${dasherize(key)}`;
        }
    }

    class Guide {
        constructor(logger) {
            this.warnedKeysByObject = new WeakMap();
            this.logger = logger;
        }
        warn(object, key, message) {
            let warnedKeys = this.warnedKeysByObject.get(object);
            if (!warnedKeys) {
                warnedKeys = new Set();
                this.warnedKeysByObject.set(object, warnedKeys);
            }
            if (!warnedKeys.has(key)) {
                warnedKeys.add(key);
                this.logger.warn(message, object);
            }
        }
    }

    function attributeValueContainsToken(attributeName, token) {
        return `[${attributeName}~="${token}"]`;
    }

    class TargetSet {
        constructor(scope) {
            this.scope = scope;
        }
        get element() {
            return this.scope.element;
        }
        get identifier() {
            return this.scope.identifier;
        }
        get schema() {
            return this.scope.schema;
        }
        has(targetName) {
            return this.find(targetName) != null;
        }
        find(...targetNames) {
            return targetNames.reduce((target, targetName) => target || this.findTarget(targetName) || this.findLegacyTarget(targetName), undefined);
        }
        findAll(...targetNames) {
            return targetNames.reduce((targets, targetName) => [
                ...targets,
                ...this.findAllTargets(targetName),
                ...this.findAllLegacyTargets(targetName),
            ], []);
        }
        findTarget(targetName) {
            const selector = this.getSelectorForTargetName(targetName);
            return this.scope.findElement(selector);
        }
        findAllTargets(targetName) {
            const selector = this.getSelectorForTargetName(targetName);
            return this.scope.findAllElements(selector);
        }
        getSelectorForTargetName(targetName) {
            const attributeName = this.schema.targetAttributeForScope(this.identifier);
            return attributeValueContainsToken(attributeName, targetName);
        }
        findLegacyTarget(targetName) {
            const selector = this.getLegacySelectorForTargetName(targetName);
            return this.deprecate(this.scope.findElement(selector), targetName);
        }
        findAllLegacyTargets(targetName) {
            const selector = this.getLegacySelectorForTargetName(targetName);
            return this.scope.findAllElements(selector).map((element) => this.deprecate(element, targetName));
        }
        getLegacySelectorForTargetName(targetName) {
            const targetDescriptor = `${this.identifier}.${targetName}`;
            return attributeValueContainsToken(this.schema.targetAttribute, targetDescriptor);
        }
        deprecate(element, targetName) {
            if (element) {
                const { identifier } = this;
                const attributeName = this.schema.targetAttribute;
                const revisedAttributeName = this.schema.targetAttributeForScope(identifier);
                this.guide.warn(element, `target:${targetName}`, `Please replace ${attributeName}="${identifier}.${targetName}" with ${revisedAttributeName}="${targetName}". ` +
                    `The ${attributeName} attribute is deprecated and will be removed in a future version of Stimulus.`);
            }
            return element;
        }
        get guide() {
            return this.scope.guide;
        }
    }

    class OutletSet {
        constructor(scope, controllerElement) {
            this.scope = scope;
            this.controllerElement = controllerElement;
        }
        get element() {
            return this.scope.element;
        }
        get identifier() {
            return this.scope.identifier;
        }
        get schema() {
            return this.scope.schema;
        }
        has(outletName) {
            return this.find(outletName) != null;
        }
        find(...outletNames) {
            return outletNames.reduce((outlet, outletName) => outlet || this.findOutlet(outletName), undefined);
        }
        findAll(...outletNames) {
            return outletNames.reduce((outlets, outletName) => [...outlets, ...this.findAllOutlets(outletName)], []);
        }
        getSelectorForOutletName(outletName) {
            const attributeName = this.schema.outletAttributeForScope(this.identifier, outletName);
            return this.controllerElement.getAttribute(attributeName);
        }
        findOutlet(outletName) {
            const selector = this.getSelectorForOutletName(outletName);
            if (selector)
                return this.findElement(selector, outletName);
        }
        findAllOutlets(outletName) {
            const selector = this.getSelectorForOutletName(outletName);
            return selector ? this.findAllElements(selector, outletName) : [];
        }
        findElement(selector, outletName) {
            const elements = this.scope.queryElements(selector);
            return elements.filter((element) => this.matchesElement(element, selector, outletName))[0];
        }
        findAllElements(selector, outletName) {
            const elements = this.scope.queryElements(selector);
            return elements.filter((element) => this.matchesElement(element, selector, outletName));
        }
        matchesElement(element, selector, outletName) {
            const controllerAttribute = element.getAttribute(this.scope.schema.controllerAttribute) || "";
            return element.matches(selector) && controllerAttribute.split(" ").includes(outletName);
        }
    }

    class Scope {
        constructor(schema, element, identifier, logger) {
            this.targets = new TargetSet(this);
            this.classes = new ClassMap(this);
            this.data = new DataMap(this);
            this.containsElement = (element) => {
                return element.closest(this.controllerSelector) === this.element;
            };
            this.schema = schema;
            this.element = element;
            this.identifier = identifier;
            this.guide = new Guide(logger);
            this.outlets = new OutletSet(this.documentScope, element);
        }
        findElement(selector) {
            return this.element.matches(selector) ? this.element : this.queryElements(selector).find(this.containsElement);
        }
        findAllElements(selector) {
            return [
                ...(this.element.matches(selector) ? [this.element] : []),
                ...this.queryElements(selector).filter(this.containsElement),
            ];
        }
        queryElements(selector) {
            return Array.from(this.element.querySelectorAll(selector));
        }
        get controllerSelector() {
            return attributeValueContainsToken(this.schema.controllerAttribute, this.identifier);
        }
        get isDocumentScope() {
            return this.element === document.documentElement;
        }
        get documentScope() {
            return this.isDocumentScope
                ? this
                : new Scope(this.schema, document.documentElement, this.identifier, this.guide.logger);
        }
    }

    class ScopeObserver {
        constructor(element, schema, delegate) {
            this.element = element;
            this.schema = schema;
            this.delegate = delegate;
            this.valueListObserver = new ValueListObserver(this.element, this.controllerAttribute, this);
            this.scopesByIdentifierByElement = new WeakMap();
            this.scopeReferenceCounts = new WeakMap();
        }
        start() {
            this.valueListObserver.start();
        }
        stop() {
            this.valueListObserver.stop();
        }
        get controllerAttribute() {
            return this.schema.controllerAttribute;
        }
        parseValueForToken(token) {
            const { element, content: identifier } = token;
            return this.parseValueForElementAndIdentifier(element, identifier);
        }
        parseValueForElementAndIdentifier(element, identifier) {
            const scopesByIdentifier = this.fetchScopesByIdentifierForElement(element);
            let scope = scopesByIdentifier.get(identifier);
            if (!scope) {
                scope = this.delegate.createScopeForElementAndIdentifier(element, identifier);
                scopesByIdentifier.set(identifier, scope);
            }
            return scope;
        }
        elementMatchedValue(element, value) {
            const referenceCount = (this.scopeReferenceCounts.get(value) || 0) + 1;
            this.scopeReferenceCounts.set(value, referenceCount);
            if (referenceCount == 1) {
                this.delegate.scopeConnected(value);
            }
        }
        elementUnmatchedValue(element, value) {
            const referenceCount = this.scopeReferenceCounts.get(value);
            if (referenceCount) {
                this.scopeReferenceCounts.set(value, referenceCount - 1);
                if (referenceCount == 1) {
                    this.delegate.scopeDisconnected(value);
                }
            }
        }
        fetchScopesByIdentifierForElement(element) {
            let scopesByIdentifier = this.scopesByIdentifierByElement.get(element);
            if (!scopesByIdentifier) {
                scopesByIdentifier = new Map();
                this.scopesByIdentifierByElement.set(element, scopesByIdentifier);
            }
            return scopesByIdentifier;
        }
    }

    class Router {
        constructor(application) {
            this.application = application;
            this.scopeObserver = new ScopeObserver(this.element, this.schema, this);
            this.scopesByIdentifier = new Multimap();
            this.modulesByIdentifier = new Map();
        }
        get element() {
            return this.application.element;
        }
        get schema() {
            return this.application.schema;
        }
        get logger() {
            return this.application.logger;
        }
        get controllerAttribute() {
            return this.schema.controllerAttribute;
        }
        get modules() {
            return Array.from(this.modulesByIdentifier.values());
        }
        get contexts() {
            return this.modules.reduce((contexts, module) => contexts.concat(module.contexts), []);
        }
        start() {
            this.scopeObserver.start();
        }
        stop() {
            this.scopeObserver.stop();
        }
        loadDefinition(definition) {
            this.unloadIdentifier(definition.identifier);
            const module = new Module(this.application, definition);
            this.connectModule(module);
            const afterLoad = definition.controllerConstructor.afterLoad;
            if (afterLoad) {
                afterLoad.call(definition.controllerConstructor, definition.identifier, this.application);
            }
        }
        unloadIdentifier(identifier) {
            const module = this.modulesByIdentifier.get(identifier);
            if (module) {
                this.disconnectModule(module);
            }
        }
        getContextForElementAndIdentifier(element, identifier) {
            const module = this.modulesByIdentifier.get(identifier);
            if (module) {
                return module.contexts.find((context) => context.element == element);
            }
        }
        proposeToConnectScopeForElementAndIdentifier(element, identifier) {
            const scope = this.scopeObserver.parseValueForElementAndIdentifier(element, identifier);
            if (scope) {
                this.scopeObserver.elementMatchedValue(scope.element, scope);
            }
            else {
                console.error(`Couldn't find or create scope for identifier: "${identifier}" and element:`, element);
            }
        }
        handleError(error, message, detail) {
            this.application.handleError(error, message, detail);
        }
        createScopeForElementAndIdentifier(element, identifier) {
            return new Scope(this.schema, element, identifier, this.logger);
        }
        scopeConnected(scope) {
            this.scopesByIdentifier.add(scope.identifier, scope);
            const module = this.modulesByIdentifier.get(scope.identifier);
            if (module) {
                module.connectContextForScope(scope);
            }
        }
        scopeDisconnected(scope) {
            this.scopesByIdentifier.delete(scope.identifier, scope);
            const module = this.modulesByIdentifier.get(scope.identifier);
            if (module) {
                module.disconnectContextForScope(scope);
            }
        }
        connectModule(module) {
            this.modulesByIdentifier.set(module.identifier, module);
            const scopes = this.scopesByIdentifier.getValuesForKey(module.identifier);
            scopes.forEach((scope) => module.connectContextForScope(scope));
        }
        disconnectModule(module) {
            this.modulesByIdentifier.delete(module.identifier);
            const scopes = this.scopesByIdentifier.getValuesForKey(module.identifier);
            scopes.forEach((scope) => module.disconnectContextForScope(scope));
        }
    }

    const defaultSchema = {
        controllerAttribute: "data-controller",
        actionAttribute: "data-action",
        targetAttribute: "data-target",
        targetAttributeForScope: (identifier) => `data-${identifier}-target`,
        outletAttributeForScope: (identifier, outlet) => `data-${identifier}-${outlet}-outlet`,
        keyMappings: Object.assign(Object.assign({ enter: "Enter", tab: "Tab", esc: "Escape", space: " ", up: "ArrowUp", down: "ArrowDown", left: "ArrowLeft", right: "ArrowRight", home: "Home", end: "End", page_up: "PageUp", page_down: "PageDown" }, objectFromEntries("abcdefghijklmnopqrstuvwxyz".split("").map((c) => [c, c]))), objectFromEntries("0123456789".split("").map((n) => [n, n]))),
    };
    function objectFromEntries(array) {
        return array.reduce((memo, [k, v]) => (Object.assign(Object.assign({}, memo), { [k]: v })), {});
    }

    class Application {
        constructor(element = document.documentElement, schema = defaultSchema) {
            this.logger = console;
            this.debug = false;
            this.logDebugActivity = (identifier, functionName, detail = {}) => {
                if (this.debug) {
                    this.logFormattedMessage(identifier, functionName, detail);
                }
            };
            this.element = element;
            this.schema = schema;
            this.dispatcher = new Dispatcher(this);
            this.router = new Router(this);
            this.actionDescriptorFilters = Object.assign({}, defaultActionDescriptorFilters);
        }
        static start(element, schema) {
            const application = new this(element, schema);
            application.start();
            return application;
        }
        async start() {
            await domReady();
            this.logDebugActivity("application", "starting");
            this.dispatcher.start();
            this.router.start();
            this.logDebugActivity("application", "start");
        }
        stop() {
            this.logDebugActivity("application", "stopping");
            this.dispatcher.stop();
            this.router.stop();
            this.logDebugActivity("application", "stop");
        }
        register(identifier, controllerConstructor) {
            this.load({ identifier, controllerConstructor });
        }
        registerActionOption(name, filter) {
            this.actionDescriptorFilters[name] = filter;
        }
        load(head, ...rest) {
            const definitions = Array.isArray(head) ? head : [head, ...rest];
            definitions.forEach((definition) => {
                if (definition.controllerConstructor.shouldLoad) {
                    this.router.loadDefinition(definition);
                }
            });
        }
        unload(head, ...rest) {
            const identifiers = Array.isArray(head) ? head : [head, ...rest];
            identifiers.forEach((identifier) => this.router.unloadIdentifier(identifier));
        }
        get controllers() {
            return this.router.contexts.map((context) => context.controller);
        }
        getControllerForElementAndIdentifier(element, identifier) {
            const context = this.router.getContextForElementAndIdentifier(element, identifier);
            return context ? context.controller : null;
        }
        handleError(error, message, detail) {
            var _a;
            this.logger.error(`%s\n\n%o\n\n%o`, message, error, detail);
            (_a = window.onerror) === null || _a === void 0 ? void 0 : _a.call(window, message, "", 0, 0, error);
        }
        logFormattedMessage(identifier, functionName, detail = {}) {
            detail = Object.assign({ application: this }, detail);
            this.logger.groupCollapsed(`${identifier} #${functionName}`);
            this.logger.log("details:", Object.assign({}, detail));
            this.logger.groupEnd();
        }
    }
    function domReady() {
        return new Promise((resolve) => {
            if (document.readyState == "loading") {
                document.addEventListener("DOMContentLoaded", () => resolve());
            }
            else {
                resolve();
            }
        });
    }

    function ClassPropertiesBlessing(constructor) {
        const classes = readInheritableStaticArrayValues(constructor, "classes");
        return classes.reduce((properties, classDefinition) => {
            return Object.assign(properties, propertiesForClassDefinition(classDefinition));
        }, {});
    }
    function propertiesForClassDefinition(key) {
        return {
            [`${key}Class`]: {
                get() {
                    const { classes } = this;
                    if (classes.has(key)) {
                        return classes.get(key);
                    }
                    else {
                        const attribute = classes.getAttributeName(key);
                        throw new Error(`Missing attribute "${attribute}"`);
                    }
                },
            },
            [`${key}Classes`]: {
                get() {
                    return this.classes.getAll(key);
                },
            },
            [`has${capitalize(key)}Class`]: {
                get() {
                    return this.classes.has(key);
                },
            },
        };
    }

    function OutletPropertiesBlessing(constructor) {
        const outlets = readInheritableStaticArrayValues(constructor, "outlets");
        return outlets.reduce((properties, outletDefinition) => {
            return Object.assign(properties, propertiesForOutletDefinition(outletDefinition));
        }, {});
    }
    function getOutletController(controller, element, identifier) {
        return controller.application.getControllerForElementAndIdentifier(element, identifier);
    }
    function getControllerAndEnsureConnectedScope(controller, element, outletName) {
        let outletController = getOutletController(controller, element, outletName);
        if (outletController)
            return outletController;
        controller.application.router.proposeToConnectScopeForElementAndIdentifier(element, outletName);
        outletController = getOutletController(controller, element, outletName);
        if (outletController)
            return outletController;
    }
    function propertiesForOutletDefinition(name) {
        const camelizedName = namespaceCamelize(name);
        return {
            [`${camelizedName}Outlet`]: {
                get() {
                    const outletElement = this.outlets.find(name);
                    const selector = this.outlets.getSelectorForOutletName(name);
                    if (outletElement) {
                        const outletController = getControllerAndEnsureConnectedScope(this, outletElement, name);
                        if (outletController)
                            return outletController;
                        throw new Error(`The provided outlet element is missing an outlet controller "${name}" instance for host controller "${this.identifier}"`);
                    }
                    throw new Error(`Missing outlet element "${name}" for host controller "${this.identifier}". Stimulus couldn't find a matching outlet element using selector "${selector}".`);
                },
            },
            [`${camelizedName}Outlets`]: {
                get() {
                    const outlets = this.outlets.findAll(name);
                    if (outlets.length > 0) {
                        return outlets
                            .map((outletElement) => {
                            const outletController = getControllerAndEnsureConnectedScope(this, outletElement, name);
                            if (outletController)
                                return outletController;
                            console.warn(`The provided outlet element is missing an outlet controller "${name}" instance for host controller "${this.identifier}"`, outletElement);
                        })
                            .filter((controller) => controller);
                    }
                    return [];
                },
            },
            [`${camelizedName}OutletElement`]: {
                get() {
                    const outletElement = this.outlets.find(name);
                    const selector = this.outlets.getSelectorForOutletName(name);
                    if (outletElement) {
                        return outletElement;
                    }
                    else {
                        throw new Error(`Missing outlet element "${name}" for host controller "${this.identifier}". Stimulus couldn't find a matching outlet element using selector "${selector}".`);
                    }
                },
            },
            [`${camelizedName}OutletElements`]: {
                get() {
                    return this.outlets.findAll(name);
                },
            },
            [`has${capitalize(camelizedName)}Outlet`]: {
                get() {
                    return this.outlets.has(name);
                },
            },
        };
    }

    function TargetPropertiesBlessing(constructor) {
        const targets = readInheritableStaticArrayValues(constructor, "targets");
        return targets.reduce((properties, targetDefinition) => {
            return Object.assign(properties, propertiesForTargetDefinition(targetDefinition));
        }, {});
    }
    function propertiesForTargetDefinition(name) {
        return {
            [`${name}Target`]: {
                get() {
                    const target = this.targets.find(name);
                    if (target) {
                        return target;
                    }
                    else {
                        throw new Error(`Missing target element "${name}" for "${this.identifier}" controller`);
                    }
                },
            },
            [`${name}Targets`]: {
                get() {
                    return this.targets.findAll(name);
                },
            },
            [`has${capitalize(name)}Target`]: {
                get() {
                    return this.targets.has(name);
                },
            },
        };
    }

    function ValuePropertiesBlessing(constructor) {
        const valueDefinitionPairs = readInheritableStaticObjectPairs(constructor, "values");
        const propertyDescriptorMap = {
            valueDescriptorMap: {
                get() {
                    return valueDefinitionPairs.reduce((result, valueDefinitionPair) => {
                        const valueDescriptor = parseValueDefinitionPair(valueDefinitionPair, this.identifier);
                        const attributeName = this.data.getAttributeNameForKey(valueDescriptor.key);
                        return Object.assign(result, { [attributeName]: valueDescriptor });
                    }, {});
                },
            },
        };
        return valueDefinitionPairs.reduce((properties, valueDefinitionPair) => {
            return Object.assign(properties, propertiesForValueDefinitionPair(valueDefinitionPair));
        }, propertyDescriptorMap);
    }
    function propertiesForValueDefinitionPair(valueDefinitionPair, controller) {
        const definition = parseValueDefinitionPair(valueDefinitionPair, controller);
        const { key, name, reader: read, writer: write } = definition;
        return {
            [name]: {
                get() {
                    const value = this.data.get(key);
                    if (value !== null) {
                        return read(value);
                    }
                    else {
                        return definition.defaultValue;
                    }
                },
                set(value) {
                    if (value === undefined) {
                        this.data.delete(key);
                    }
                    else {
                        this.data.set(key, write(value));
                    }
                },
            },
            [`has${capitalize(name)}`]: {
                get() {
                    return this.data.has(key) || definition.hasCustomDefaultValue;
                },
            },
        };
    }
    function parseValueDefinitionPair([token, typeDefinition], controller) {
        return valueDescriptorForTokenAndTypeDefinition({
            controller,
            token,
            typeDefinition,
        });
    }
    function parseValueTypeConstant(constant) {
        switch (constant) {
            case Array:
                return "array";
            case Boolean:
                return "boolean";
            case Number:
                return "number";
            case Object:
                return "object";
            case String:
                return "string";
        }
    }
    function parseValueTypeDefault(defaultValue) {
        switch (typeof defaultValue) {
            case "boolean":
                return "boolean";
            case "number":
                return "number";
            case "string":
                return "string";
        }
        if (Array.isArray(defaultValue))
            return "array";
        if (Object.prototype.toString.call(defaultValue) === "[object Object]")
            return "object";
    }
    function parseValueTypeObject(payload) {
        const { controller, token, typeObject } = payload;
        const hasType = isSomething(typeObject.type);
        const hasDefault = isSomething(typeObject.default);
        const fullObject = hasType && hasDefault;
        const onlyType = hasType && !hasDefault;
        const onlyDefault = !hasType && hasDefault;
        const typeFromObject = parseValueTypeConstant(typeObject.type);
        const typeFromDefaultValue = parseValueTypeDefault(payload.typeObject.default);
        if (onlyType)
            return typeFromObject;
        if (onlyDefault)
            return typeFromDefaultValue;
        if (typeFromObject !== typeFromDefaultValue) {
            const propertyPath = controller ? `${controller}.${token}` : token;
            throw new Error(`The specified default value for the Stimulus Value "${propertyPath}" must match the defined type "${typeFromObject}". The provided default value of "${typeObject.default}" is of type "${typeFromDefaultValue}".`);
        }
        if (fullObject)
            return typeFromObject;
    }
    function parseValueTypeDefinition(payload) {
        const { controller, token, typeDefinition } = payload;
        const typeObject = { controller, token, typeObject: typeDefinition };
        const typeFromObject = parseValueTypeObject(typeObject);
        const typeFromDefaultValue = parseValueTypeDefault(typeDefinition);
        const typeFromConstant = parseValueTypeConstant(typeDefinition);
        const type = typeFromObject || typeFromDefaultValue || typeFromConstant;
        if (type)
            return type;
        const propertyPath = controller ? `${controller}.${typeDefinition}` : token;
        throw new Error(`Unknown value type "${propertyPath}" for "${token}" value`);
    }
    function defaultValueForDefinition(typeDefinition) {
        const constant = parseValueTypeConstant(typeDefinition);
        if (constant)
            return defaultValuesByType[constant];
        const hasDefault = hasProperty(typeDefinition, "default");
        const hasType = hasProperty(typeDefinition, "type");
        const typeObject = typeDefinition;
        if (hasDefault)
            return typeObject.default;
        if (hasType) {
            const { type } = typeObject;
            const constantFromType = parseValueTypeConstant(type);
            if (constantFromType)
                return defaultValuesByType[constantFromType];
        }
        return typeDefinition;
    }
    function valueDescriptorForTokenAndTypeDefinition(payload) {
        const { token, typeDefinition } = payload;
        const key = `${dasherize(token)}-value`;
        const type = parseValueTypeDefinition(payload);
        return {
            type,
            key,
            name: camelize(key),
            get defaultValue() {
                return defaultValueForDefinition(typeDefinition);
            },
            get hasCustomDefaultValue() {
                return parseValueTypeDefault(typeDefinition) !== undefined;
            },
            reader: readers[type],
            writer: writers[type] || writers.default,
        };
    }
    const defaultValuesByType = {
        get array() {
            return [];
        },
        boolean: false,
        number: 0,
        get object() {
            return {};
        },
        string: "",
    };
    const readers = {
        array(value) {
            const array = JSON.parse(value);
            if (!Array.isArray(array)) {
                throw new TypeError(`expected value of type "array" but instead got value "${value}" of type "${parseValueTypeDefault(array)}"`);
            }
            return array;
        },
        boolean(value) {
            return !(value == "0" || String(value).toLowerCase() == "false");
        },
        number(value) {
            return Number(value.replace(/_/g, ""));
        },
        object(value) {
            const object = JSON.parse(value);
            if (object === null || typeof object != "object" || Array.isArray(object)) {
                throw new TypeError(`expected value of type "object" but instead got value "${value}" of type "${parseValueTypeDefault(object)}"`);
            }
            return object;
        },
        string(value) {
            return value;
        },
    };
    const writers = {
        default: writeString,
        array: writeJSON,
        object: writeJSON,
    };
    function writeJSON(value) {
        return JSON.stringify(value);
    }
    function writeString(value) {
        return `${value}`;
    }

    class Controller {
        constructor(context) {
            this.context = context;
        }
        static get shouldLoad() {
            return true;
        }
        static afterLoad(_identifier, _application) {
            return;
        }
        get application() {
            return this.context.application;
        }
        get scope() {
            return this.context.scope;
        }
        get element() {
            return this.scope.element;
        }
        get identifier() {
            return this.scope.identifier;
        }
        get targets() {
            return this.scope.targets;
        }
        get outlets() {
            return this.scope.outlets;
        }
        get classes() {
            return this.scope.classes;
        }
        get data() {
            return this.scope.data;
        }
        initialize() {
        }
        connect() {
        }
        disconnect() {
        }
        dispatch(eventName, { target = this.element, detail = {}, prefix = this.identifier, bubbles = true, cancelable = true, } = {}) {
            const type = prefix ? `${prefix}:${eventName}` : eventName;
            const event = new CustomEvent(type, { detail, bubbles, cancelable });
            target.dispatchEvent(event);
            return event;
        }
    }
    Controller.blessings = [
        ClassPropertiesBlessing,
        TargetPropertiesBlessing,
        ValuePropertiesBlessing,
        OutletPropertiesBlessing,
    ];
    Controller.targets = [];
    Controller.outlets = [];
    Controller.values = {};

    exports.Application = Application;
    exports.AttributeObserver = AttributeObserver;
    exports.Context = Context;
    exports.Controller = Controller;
    exports.ElementObserver = ElementObserver;
    exports.IndexedMultimap = IndexedMultimap;
    exports.Multimap = Multimap;
    exports.SelectorObserver = SelectorObserver;
    exports.StringMapObserver = StringMapObserver;
    exports.TokenListObserver = TokenListObserver;
    exports.ValueListObserver = ValueListObserver;
    exports.add = add;
    exports.defaultSchema = defaultSchema;
    exports.del = del;
    exports.fetch = fetch;
    exports.prune = prune;

    Object.defineProperty(exports, '__esModule', { value: true });

})));

;

"use strict";
(() => {
  // internal/httpui/static/src/shared.ts
  function announce(message) {
    const region = document.getElementById("ui-announcer");
    if (region) region.textContent = message;
  }
  function formatBytes(value) {
    let bytes = Math.max(0, Number(value) || 0);
    const units = ["B", "KiB", "MiB", "GiB", "TiB", "PiB"];
    let unit = 0;
    while (bytes >= 1024 && unit < units.length - 1) {
      bytes /= 1024;
      unit += 1;
    }
    const digits = unit === 0 ? 0 : bytes >= 10 ? 1 : 2;
    return `${bytes.toFixed(digits)} ${units[unit]}`;
  }
  function decodeModel(encoded) {
    const binary = atob(encoded || "");
    const bytes = Uint8Array.from(binary, (character) => character.charCodeAt(0));
    return JSON.parse(new TextDecoder().decode(bytes));
  }
  function dispatchRevision(detail) {
    document.dispatchEvent(new CustomEvent("stewarr:revision", { detail }));
  }

  // internal/httpui/static/src/controllers/revisions.ts
  var maxReconnectAttempts = 5;
  var RevisionsController = class extends window.Stimulus.Controller {
    constructor() {
      super(...arguments);
      this.etag = "";
      this.pollTimer = null;
      this.reconnectTimer = null;
      this.events = null;
      // The server voluntarily ends and lets the client reconnect every few
      // minutes (a connection can go silently dead through a network path with
      // neither side ever seeing an error, so relying on failure detection
      // alone isn't enough) — EventSource has no way to tell "the server closed
      // this on purpose" apart from "the network died", both surface as the
      // same error event. Counting consecutive failures, and resetting the
      // count on every successful open, is what keeps a routine server-side
      // rotation from permanently downgrading every page load to polling after
      // a few minutes.
      this.reconnectAttempts = 0;
      // A newly opened EventSource always immediately replays whatever revision
      // is already current — not just on the page's very first connection, but
      // on every reconnect too (the server's own periodic rotation, or a
      // flaky network path retrying every few seconds). That replay carries
      // whatever revision number the server last published, so without
      // tracking it a routine reconnect looks identical to a genuine live
      // change and re-triggers "Updates available" for nothing actually new.
      // null means "nothing processed yet this page load"; only a strictly
      // higher revision number than the last one seen counts as real.
      this.lastRevision = null;
    }
    connect() {
      this.etag = "";
      this.pollTimer = null;
      this.reconnectTimer = null;
      this.reconnectAttempts = 0;
      this.lastRevision = null;
      this.onVisible = () => {
        if (!document.hidden) this.refreshStatus({ kind: "visibility" });
      };
      document.addEventListener("visibilitychange", this.onVisible);
      this.onPageHide = () => {
        if (this.events) {
          this.events.close();
          this.events = null;
        }
      };
      window.addEventListener("pagehide", this.onPageHide);
      this.connectEvents();
    }
    disconnect() {
      document.removeEventListener("visibilitychange", this.onVisible);
      window.removeEventListener("pagehide", this.onPageHide);
      if (this.events) this.events.close();
      if (this.pollTimer) clearInterval(this.pollTimer);
      if (this.reconnectTimer) clearTimeout(this.reconnectTimer);
    }
    connectEvents() {
      if (!("EventSource" in window)) {
        this.startPolling();
        return;
      }
      if (this.events) this.events.close();
      this.events = new EventSource("/ui/events");
      this.events.addEventListener("revision", (event) => {
        try {
          this.refreshStatus(JSON.parse(event.data));
        } catch (_) {
          this.refreshStatus({ kind: "background" });
        }
      });
      this.events.onopen = () => {
        this.reconnectAttempts = 0;
        if (this.pollTimer) {
          clearInterval(this.pollTimer);
          this.pollTimer = null;
        }
      };
      this.events.onerror = () => {
        if (this.events) {
          this.events.close();
          this.events = null;
        }
        this.reconnectAttempts++;
        if (this.reconnectAttempts > maxReconnectAttempts) {
          this.startPolling();
          return;
        }
        const delay = Math.min(500 * 2 ** (this.reconnectAttempts - 1), 8e3);
        this.reconnectTimer = setTimeout(() => this.connectEvents(), delay);
      };
    }
    startPolling() {
      if (this.pollTimer) return;
      this.pollTimer = setInterval(() => {
        if (!document.hidden) this.refreshStatus({ kind: "poll" });
      }, 5e3);
    }
    async refreshStatus(invalidation) {
      const headers = {};
      if (this.etag) headers["If-None-Match"] = this.etag;
      try {
        const response = await fetch("/ui/status", { headers, cache: "no-store" });
        if (response.status === 304) return;
        if (!response.ok) throw new Error(`status ${response.status}`);
        this.etag = response.headers.get("ETag") || "";
        const status = await response.json();
        this.renderStatus(status);
        const isPushInvalidation = invalidation.kind !== "poll" && invalidation.kind !== "visibility";
        if (isPushInvalidation) {
          if (this.lastRevision === null) {
            if (typeof invalidation.revision === "number") this.lastRevision = invalidation.revision;
            return;
          }
          if (typeof invalidation.revision === "number") {
            if (invalidation.revision <= this.lastRevision) return;
            this.lastRevision = invalidation.revision;
          }
        }
        const sourceKind = invalidation.kind === "poll" || invalidation.kind === "visibility" ? status.kind : invalidation.kind;
        dispatchRevision({ ...status, kind: sourceKind || status.kind });
      } catch (_) {
        this.startPolling();
      }
    }
    renderStatus(status) {
      const indicator = document.getElementById("operation-indicator");
      if (indicator) {
        const count = Number(status.pendingOperations) || 0;
        indicator.hidden = count === 0;
        indicator.textContent = count === 1 ? "1 operation in progress" : `${count} operations in progress`;
      }
      const notices = document.getElementById("persistent-notices");
      if (notices) {
        notices.replaceChildren(...(status.notices || []).map((notice) => {
          const item = document.createElement("a");
          item.className = "persistent-notice bad";
          item.href = `/history#operation-${notice.id}`;
          item.textContent = `${notice.label || "Removal"}: ${notice.message || notice.status}`;
          return item;
        }));
      }
    }
  };

  // internal/httpui/static/src/controllers/updates.ts
  var UpdatesController = class extends window.Stimulus.Controller {
    apply() {
      this.element.hidden = true;
      document.dispatchEvent(new CustomEvent("stewarr:apply-updates"));
    }
  };

  // internal/httpui/static/src/controllers/dashboard.ts
  var DashboardController = class extends window.Stimulus.Controller {
    constructor() {
      super(...arguments);
      this.etag = "";
    }
    connect() {
      this.etag = "";
      this.onRevision = (event) => {
        const kind = String(event.detail?.kind || "background");
        if (kind === "tasks") return;
        this.refresh();
      };
      document.addEventListener("stewarr:revision", this.onRevision);
    }
    disconnect() {
      document.removeEventListener("stewarr:revision", this.onRevision);
    }
    field(name) {
      return this.element.querySelector(`[data-dashboard-field="${name}"]`);
    }
    set(name, value) {
      const element = this.field(name);
      if (element) element.textContent = String(value);
    }
    async refresh() {
      const headers = {};
      if (this.etag) headers["If-None-Match"] = this.etag;
      try {
        const response = await fetch("/api/dashboard", { headers, cache: "no-store" });
        if (response.status === 304) return;
        if (!response.ok) throw new Error(`status ${response.status}`);
        this.etag = response.headers.get("ETag") || "";
        const data = await response.json();
        for (const name of ["totalMedia", "movies", "series", "libraryBytes", "totalTorrents", "current", "superseded", "orphaned", "unassociated", "obsoleteReclaimable"]) this.set(name, data[name]);
        const stats = data.stats || {};
        this.set("reclaimedBytes", formatBytes(stats.ReclaimedBytes));
        this.set("runs", stats.Runs || 0);
        this.set("mediaRemoved", stats.MediaRemoved || 0);
        this.set("torrentsRemoved", stats.TorrentsRemoved || 0);
        this.set("mediaBytes", formatBytes(stats.MediaBytes));
        this.set("last30Bytes", formatBytes(stats.Last30Bytes));
      } catch (_) {
      }
    }
  };

  // internal/httpui/static/src/controllers/shell.ts
  function humanBytes(bytes) {
    const units = ["B", "KiB", "MiB", "GiB", "TiB"];
    let value = bytes;
    let unitIndex = 0;
    while (value >= 1024 && unitIndex < units.length - 1) {
      value /= 1024;
      unitIndex++;
    }
    return `${value.toFixed(1)} ${units[unitIndex]}`;
  }
  var ShellController = class extends window.Stimulus.Controller {
    constructor() {
      super(...arguments);
      this.filterTimer = null;
      this.modalRequest = null;
    }
    connect() {
      this.filterTimer = null;
      this.modalRequest = null;
      this.onClick = (event) => this.click(event);
      this.onInput = (event) => this.filterInput(event);
      this.onChange = (event) => this.filterChange(event);
      this.onSubmit = (event) => this.submit(event);
      this.onRevision = (event) => this.revision(event.detail || {});
      this.onApply = () => this.refreshFragments(true, true);
      this.onAccepted = () => this.refreshFragments(true, true);
      document.addEventListener("click", this.onClick);
      document.addEventListener("input", this.onInput);
      document.addEventListener("change", this.onChange);
      document.addEventListener("submit", this.onSubmit);
      document.addEventListener("stewarr:revision", this.onRevision);
      document.addEventListener("stewarr:apply-updates", this.onApply);
      document.addEventListener("stewarr:mutation-accepted", this.onAccepted);
    }
    disconnect() {
      document.removeEventListener("click", this.onClick);
      document.removeEventListener("input", this.onInput);
      document.removeEventListener("change", this.onChange);
      document.removeEventListener("submit", this.onSubmit);
      document.removeEventListener("stewarr:revision", this.onRevision);
      document.removeEventListener("stewarr:apply-updates", this.onApply);
      document.removeEventListener("stewarr:mutation-accepted", this.onAccepted);
      if (this.filterTimer) clearTimeout(this.filterTimer);
      if (this.modalRequest) this.modalRequest.abort();
    }
    click(event) {
      const target = event.target;
      const removal = target.closest("[data-removal-url]");
      if (removal) {
        event.preventDefault();
        this.openOverlay(removal.dataset.removalUrl, removal);
        return;
      }
      const overlay = target.closest("[data-overlay-url]");
      if (overlay) {
        event.preventDefault();
        this.openOverlay(overlay.dataset.overlayUrl, overlay);
        return;
      }
      const loadingCancel = target.closest("[data-modal-cancel-loading]");
      if (loadingCancel) {
        event.preventDefault();
        if (this.modalRequest) this.modalRequest.abort();
        this.closeModal();
        return;
      }
      const overlayCancel = target.closest("[data-overlay-cancel]");
      if (overlayCancel) {
        event.preventDefault();
        this.closeModal();
        return;
      }
      const clear = target.closest("[data-filter-clear]");
      if (clear) {
        event.preventDefault();
        const form = clear.closest("body")?.querySelector("form[data-auto-filter]");
        if (form) {
          form.reset();
          this.navigateList(clear.href);
        }
        return;
      }
      const listLink = target.closest("[data-filter-results] a[href]:not([data-list-item-link])");
      if (listLink && listLink.origin === location.origin) {
        event.preventDefault();
        this.navigateList(listLink.href);
        return;
      }
      const all = target.closest("#unmanagedAll");
      if (all) {
        document.querySelectorAll(".unmanagedPick").forEach((input) => {
          input.checked = all.checked;
        });
        this.syncUnmanagedSelection();
      }
      const serviceTest = target.closest("[data-service-test]");
      if (serviceTest) {
        event.preventDefault();
        this.testServiceConnection(serviceTest);
      }
      const tmdbTest = target.closest("[data-tmdb-test]");
      if (tmdbTest) {
        event.preventDefault();
        this.testTMDBConnection(tmdbTest);
      }
    }
    filterInput(event) {
      const target = event.target;
      const form = target.closest("form[data-auto-filter]");
      if (!form || target.type !== "search") return;
      if (this.filterTimer) clearTimeout(this.filterTimer);
      this.filterTimer = setTimeout(() => this.navigateList(this.formURL(form)), 180);
    }
    filterChange(event) {
      const target = event.target;
      if (target.matches("[data-service-type]")) {
        this.syncServiceFields(target);
        return;
      }
      if (target.matches(".unmanagedPick")) {
        this.syncUnmanagedSelection();
        return;
      }
      if (target.matches("[data-tmdb-toggle]")) {
        this.syncTMDBFields(target);
        return;
      }
      if (target.matches("[data-automatic-removal-toggle]")) {
        this.syncThresholdFields(target);
        return;
      }
      const form = target.closest("form[data-auto-filter]");
      if (form) this.navigateList(this.formURL(form));
    }
    // Each service type only needs a subset of credential fields (an API
    // key, or a username+password, never both) — show only the ones that
    // apply to whatever type is currently selected in the Add service form.
    syncServiceFields(select) {
      const form = select.closest("form");
      if (!form) return;
      const type = select.value;
      for (const field of form.querySelectorAll("[data-service-field]")) {
        const types = (field.dataset.serviceField || "").split(/\s+/);
        field.hidden = !types.includes(type);
      }
    }
    // Hides the API key field (and Test button) when TMDB enrichment is
    // switched off, and clears the key's value so submitting the form in
    // that state actually disables it server-side rather than resubmitting
    // whatever was last saved — there is no separate on/off switch,
    // clearing the key IS how it's disabled.
    syncTMDBFields(toggle) {
      const form = toggle.closest("form");
      if (!form) return;
      const enabled = toggle.checked;
      for (const field of form.querySelectorAll("[data-tmdb-field]")) field.hidden = !enabled;
      if (!enabled) {
        const key = form.querySelector("#settings-tmdb-key");
        if (key) key.value = "";
      }
    }
    // Hides the Target/Critical fields when automatic removal is disabled for
    // this device — they have no effect once nothing evaluates them, so
    // showing them would suggest a threshold is in force when none is.
    syncThresholdFields(toggle) {
      const form = toggle.closest("form");
      if (!form) return;
      const enabled = toggle.checked;
      for (const field of form.querySelectorAll("[data-threshold-field]")) field.hidden = !enabled;
    }
    async testTMDBConnection(button) {
      const form = button.closest("form");
      if (!form) return;
      const resultTarget = form.querySelector("[data-tmdb-test-result]");
      const setResult = (text, className) => {
        if (!resultTarget) return;
        resultTarget.textContent = text;
        resultTarget.className = className;
      };
      button.disabled = true;
      const originalLabel = button.textContent;
      button.textContent = "Testing\u2026";
      setResult("", "");
      try {
        const response = await fetch("/settings/tmdb/test", { method: "POST", body: new FormData(form) });
        if (!response.ok) throw new Error((await response.text()).trim() || `status ${response.status}`);
        setResult("Connection verified.", "positive");
      } catch (error) {
        setResult(error instanceof Error ? error.message : String(error), "bad");
      } finally {
        button.disabled = false;
        button.textContent = originalLabel;
      }
    }
    async submit(event) {
      const form = event.target;
      if (!(form instanceof HTMLFormElement)) return;
      if (form.matches("[data-removal-launch]")) {
        event.preventDefault();
        const url = new URL(form.action, location.href);
        for (const [name, value] of new FormData(form)) url.searchParams.append(name, value);
        this.openOverlay(url.href, form.querySelector("button[type=submit]"));
        return;
      }
      const thenOverlayURL = form.dataset.backgroundSubmitThenOverlay;
      if (form.matches("[data-background-submit], [data-background-submit-then-overlay]")) {
        event.preventDefault();
        const button = form.querySelector("button[type=submit]");
        if (button) button.disabled = true;
        const errorTarget = form.querySelector("[data-modal-error]");
        if (errorTarget) errorTarget.hidden = true;
        try {
          const response = await fetch(form.action, { method: form.method || "POST", body: new FormData(form) });
          if (!response.ok) throw new Error((await response.text()).trim() || `status ${response.status}`);
          if (thenOverlayURL) {
            this.openOverlay(thenOverlayURL, button || form);
          } else {
            const insideModal = form.closest("#modal-root");
            if (insideModal) {
              insideModal.replaceChildren();
              document.body.classList.remove("modal-open");
            }
          }
          dispatchRevision({ kind: "operation" });
        } catch (error) {
          if (errorTarget) {
            errorTarget.textContent = error.message;
            errorTarget.hidden = false;
          } else {
            announce(`Action failed: ${error.message}`);
          }
        } finally {
          if (button) button.disabled = false;
        }
      }
    }
    // Step 1 of the service setup overlay: a live connection check with
    // nothing saved yet. Only on success does the real "Add service" submit
    // appear — storage roots are always discovered by the service's own
    // adapter after saving, never entered by hand, so there is no step 2
    // field to reveal here.
    async testServiceConnection(button) {
      const form = button.closest("form");
      if (!form) return;
      const hint = form.closest(".modal-dialog")?.querySelector("[data-service-step-hint]");
      const errorTarget = form.querySelector("[data-modal-error]");
      if (errorTarget) errorTarget.hidden = true;
      button.disabled = true;
      const originalLabel = button.textContent;
      button.textContent = "Testing\u2026";
      try {
        const response = await fetch("/services/test", { method: "POST", body: new FormData(form) });
        if (!response.ok) throw new Error((await response.text()).trim() || `status ${response.status}`);
        const submitButton = form.querySelector("[data-service-submit]");
        if (submitButton) submitButton.hidden = false;
        button.hidden = true;
        if (hint) hint.textContent = "Connection verified. Save to finish.";
      } catch (error) {
        if (errorTarget) {
          errorTarget.textContent = error.message;
          errorTarget.hidden = false;
        } else {
          announce(`Connection test failed: ${error.message}`);
        }
      } finally {
        button.disabled = false;
        button.textContent = originalLabel;
      }
    }
    // One visible .unmanagedPick checkbox per physical file, regardless of
    // how many hardlinked paths it has — selecting it mirrors onto every
    // hidden .unmanagedGroupPath (one per path, all name="path") sharing its
    // data-unmanaged-group. Partial selection (only some of a file's
    // hardlinks) was tried and rejected: it silently reclaims 0 bytes, since
    // the remaining link keeps the data alive, with no visible reason why.
    syncUnmanagedSelection() {
      const picks = [...document.querySelectorAll(".unmanagedPick")];
      for (const pick of picks) {
        document.querySelectorAll(`.unmanagedGroupPath[data-unmanaged-group="${CSS.escape(pick.dataset.unmanagedGroup)}"]`).forEach((input) => {
          input.checked = pick.checked;
        });
      }
      const selected = picks.filter((input) => input.checked).length;
      const all = document.getElementById("unmanagedAll");
      if (all) {
        all.checked = picks.length > 0 && selected === picks.length;
        all.indeterminate = selected > 0 && selected < picks.length;
      }
      const button = document.getElementById("unmanagedRemoveButton");
      if (button) button.disabled = selected === 0;
    }
    formURL(form) {
      const url = new URL(form.action, location.href);
      const values = new FormData(form);
      for (const [name, value] of values) url.searchParams.append(name, value);
      return url.href;
    }
    navigateList(url) {
      const fragment = document.querySelector("[data-filter-results]");
      if (!fragment || !fragment.id) return;
      history.replaceState({}, "", url);
      this.swapFragment(fragment, url, true);
      const updates = document.getElementById("updates-available");
      if (updates) updates.hidden = true;
    }
    // userTriggered marks the swap with [data-user-triggered-swap] so the
    // CSS loading dip (app.css) applies — passive/background swaps (revision
    // pushes, polling) are the normal case and stay silent by default; only a
    // swap the user directly asked for opts in to the visual feedback.
    swapFragment(fragment, url = location.href, userTriggered = false) {
      if (userTriggered) fragment.setAttribute("data-user-triggered-swap", "");
      return window.htmx.ajax("GET", url, {
        source: fragment,
        target: `#${CSS.escape(fragment.id)}`,
        select: `#${CSS.escape(fragment.id)}`,
        swap: "outerHTML"
      });
    }
    revision(detail) {
      this.refreshWizardProgress();
      const kind = String(detail.kind || "background");
      const urgent = /mutation|operation|removal|failure/.test(kind);
      if (kind === "storage" && document.querySelector("[data-storage-summary]")) {
        this.refreshStorageStats();
        return;
      }
      const stable = document.querySelector('[data-live-policy="stable-list"]');
      if (stable && (kind === "tasks" || kind === "startup" || kind === "storage")) return;
      if (stable && !urgent) {
        const updates = document.getElementById("updates-available");
        if (updates) updates.hidden = false;
        return;
      }
      this.refreshFragments(urgent, false);
    }
    // Step 3 of the service setup overlay has no push mechanism of its own —
    // it piggybacks on the SSE revision stream that already fires on every
    // task-manager change (including this workflow's own step advances), and
    // just re-fetches the current status on each tick, in place, without a
    // full fragment re-render.
    async refreshWizardProgress() {
      const root = document.querySelector("[data-wizard-progress]");
      if (!root) return;
      try {
        const response = await fetch("/services/consistency-status");
        if (!response.ok) return;
        const status = await response.json();
        const label = root.querySelector("[data-wizard-progress-label]");
        const fill = root.querySelector(".wizard-progress-bar-fill");
        if (label) {
          label.textContent = status.state === "succeeded" || status.state === "attention" ? "Done." : `Step ${status.currentStep + 1} of ${status.totalSteps}: ${status.stepLabel || "Working\u2026"}`;
        }
        if (fill) fill.classList.toggle("done", status.state === "succeeded" || status.state === "attention");
      } catch (_) {
      }
    }
    // The Storage page's "used of total" line updates straight from the cheap
    // /storage/stats endpoint (raw statfs bytes only — no removal plan) rather
    // than through the fragment-swap machinery, so a byte-level tick every few
    // seconds never re-renders the bar/legend/plan or touches the threshold
    // form at all.
    async refreshStorageStats() {
      try {
        const response = await fetch("/storage/stats", { cache: "no-store" });
        if (!response.ok) return;
        const devices = await response.json();
        for (const device of devices) {
          if (!device.available) continue;
          const summary = document.querySelector(
            `[data-storage-summary][data-representative-path="${CSS.escape(device.representativePath)}"]`
          );
          if (!summary) continue;
          const unexplainedOther = device.otherBytes > device.reservedBytes ? device.otherBytes - device.reservedBytes : 0;
          const reserved = device.reservedBytes > 0 ? ` \xB7 ${humanBytes(device.reservedBytes)} reserved by the filesystem` : "";
          const elsewhere = unexplainedOther > 0 ? ` \xB7 ${humanBytes(unexplainedOther)} used elsewhere on this ${humanBytes(device.totalBytes)} disk` : "";
          summary.textContent = `${humanBytes(device.stewarrUsedBytes)} used of ${humanBytes(device.usableBytes)} usable (${device.usagePercentOfUsable.toFixed(1)}%, target ${device.targetUsagePercent.toFixed(1)}%)${reserved}${elsewhere}`;
        }
      } catch (_) {
      }
    }
    refreshFragments(force, userTriggered = false) {
      const fragments = [...document.querySelectorAll("[data-reactive-fragment][id]")];
      for (const fragment of fragments) {
        if (fragment.dataset.livePolicy === "stable-list" && !force) continue;
        this.swapFragment(fragment, location.href, userTriggered);
      }
      const updates = document.getElementById("updates-available");
      if (updates && force) updates.hidden = true;
    }
    async openOverlay(url, opener) {
      if (this.modalRequest) this.modalRequest.abort();
      this.modalRequest = new AbortController();
      const root = document.getElementById("modal-root");
      if (!root) return;
      root.innerHTML = '<div class="modal-overlay"><main class="modal-dialog preparing" role="dialog" aria-modal="true"><p class="muted">Loading\u2026</p><div class="actions"><button type="button" data-modal-cancel-loading>Cancel</button></div></main></div>';
      root.dataset.openerId = opener.id || "";
      root._stewarrOpener = opener;
      document.body.classList.add("modal-open");
      try {
        const response = await fetch(url, { signal: this.modalRequest.signal, headers: { "X-Stewarr-Overlay": "1" } });
        const content = await response.text();
        if (!response.ok) throw new Error(content.trim() || `status ${response.status}`);
        root.innerHTML = content;
        window.htmx.process(root);
        this.refreshWizardProgress();
        const serviceType = root.querySelector("[data-service-type]");
        if (serviceType) this.syncServiceFields(serviceType);
      } catch (error) {
        if (error.name === "AbortError") return;
        root.innerHTML = `<div class="modal-overlay"><main class="modal-dialog" role="dialog" aria-modal="true"><p class="bad"></p><div class="actions"><button type="button" data-modal-cancel-loading>Close</button></div></main></div>`;
        root.querySelector(".bad").textContent = `Unavailable: ${error.message}`;
      } finally {
        this.modalRequest = null;
      }
    }
    closeModal() {
      const root = document.getElementById("modal-root");
      if (!root) return;
      const opener = root._stewarrOpener;
      root.replaceChildren();
      document.body.classList.remove("modal-open");
      if (opener && document.contains(opener)) opener.focus();
    }
  };

  // internal/httpui/static/src/controllers/removal.ts
  var RemovalController = class extends window.Stimulus.Controller {
    constructor() {
      super(...arguments);
      this.busy = false;
    }
    connect() {
      this.model = decodeModel(this.element.dataset.removalModelValue);
      this.busy = false;
      this.actionSelections = new Set((this.model.files || []).filter((file) => file.selectable && file.selected).map((file) => `${file.actionName}\0${file.actionValue}`));
      this.escapeHandler = (event) => {
        if (event.key === "Escape" && !this.busy) this.cancel();
      };
      document.addEventListener("keydown", this.escapeHandler);
      document.body.classList.add("modal-open");
      this.updateGroupStates();
      this.calculate();
      requestAnimationFrame(() => this.element.querySelector(".modal-dialog")?.focus());
    }
    disconnect() {
      document.removeEventListener("keydown", this.escapeHandler);
    }
    cancel() {
      if (this.busy) return;
      const root = document.getElementById("modal-root");
      const opener = root?._stewarrOpener;
      root?.replaceChildren();
      document.body.classList.remove("modal-open");
      if (opener && document.contains(opener)) opener.focus();
    }
    backdrop(event) {
      if (event.target === this.element && !this.busy) this.cancel();
    }
    selectionChanged(event) {
      const checkbox = event.target;
      if (!(checkbox instanceof HTMLInputElement) || checkbox.type !== "checkbox") return;
      let selector = "";
      let scope = null;
      if (checkbox.matches("[data-managed-group-all]")) {
        scope = checkbox.closest("[data-managed-group]");
        selector = "[data-managed-pick]";
      } else if (checkbox.matches("[data-managed-all]")) {
        scope = checkbox.closest("[data-managed-scope]");
        selector = "[data-managed-pick]";
      } else if (checkbox.matches("[data-select-all]")) {
        scope = checkbox.closest("[data-linked-scope]");
        selector = "[data-linked-pick]";
      } else if (checkbox.matches("[data-unmanaged-all]")) {
        scope = checkbox.closest("[data-unmanaged-scope]");
        selector = "[data-unmanaged-pick]";
      }
      if (scope && selector) {
        scope.querySelectorAll(selector).forEach((input) => {
          input.checked = checkbox.checked;
          this.setControlSelection(input, checkbox.checked);
        });
      } else {
        this.setControlSelection(checkbox, checkbox.checked);
      }
      this.updateGroupStates();
      this.calculate();
    }
    setControlSelection(input, selected) {
      if (input.matches("[data-physical-pick]")) {
        const physicalKey = input.dataset.physicalKey;
        for (const file of this.model.files || []) {
          if (!file.selectable || file.physicalKey !== physicalKey) continue;
          const key2 = `${file.actionName}\0${file.actionValue}`;
          if (selected) this.actionSelections.add(key2);
          else this.actionSelections.delete(key2);
        }
        return;
      }
      if (!input.name) return;
      const key = `${input.name}\0${input.value}`;
      if (selected) this.actionSelections.add(key);
      else this.actionSelections.delete(key);
    }
    updateGroupStates() {
      const setState = (master, inputs) => {
        if (!master || inputs.length === 0) return;
        const selected = inputs.filter((input) => input.checked).length;
        master.checked = selected === inputs.length;
        master.indeterminate = selected > 0 && selected < inputs.length;
      };
      const actions = this.selectedActionKeys();
      this.selectionTarget?.querySelectorAll('input[name][type="checkbox"]:not([data-generated-input])').forEach((input) => {
        input.checked = actions.has(`${input.name}\0${input.value}`);
      });
      this.element.querySelectorAll("[data-physical-pick]").forEach((checkbox) => {
        const keys = new Set((this.model.files || []).filter((file) => file.selectable && file.physicalKey === checkbox.dataset.physicalKey).map((file) => `${file.actionName}\0${file.actionValue}`));
        const selected = [...keys].filter((key) => actions.has(key)).length;
        if (keys.size === 0) return;
        checkbox.checked = selected === keys.size;
        checkbox.indeterminate = selected > 0 && selected < keys.size;
      });
      this.element.querySelectorAll("[data-managed-group]").forEach((scope) => setState(scope.querySelector("[data-managed-group-all]"), [...scope.querySelectorAll("[data-managed-pick]")]));
      this.element.querySelectorAll("[data-managed-scope]").forEach((scope) => setState(scope.querySelector("[data-managed-all]"), [...scope.querySelectorAll("[data-managed-pick]")]));
      this.element.querySelectorAll("[data-linked-scope]").forEach((scope) => setState(scope.querySelector("[data-select-all]"), [...scope.querySelectorAll("[data-linked-pick]")]));
      this.element.querySelectorAll("[data-unmanaged-scope]").forEach((scope) => setState(scope.querySelector("[data-unmanaged-all]"), [...scope.querySelectorAll("[data-unmanaged-pick]")]));
    }
    selectedActionKeys() {
      const selected = new Set(this.actionSelections || []);
      for (const file of this.model.files || []) {
        if (file.always) selected.add(`${file.actionName}\0${file.actionValue}`);
      }
      return selected;
    }
    calculate() {
      const actions = this.selectedActionKeys();
      const groups = /* @__PURE__ */ new Map();
      const logicalMedia = /* @__PURE__ */ new Map();
      const selectedFacts = [];
      for (const file of this.model.files || []) {
        const selected = file.always || actions.has(`${file.actionName}\0${file.actionValue}`);
        file.selected = selected;
        if (selected) selectedFacts.push(file);
        if (selected && file.owner === "media") {
          const logicalKey = file.identityKnown ? `${file.device}:${file.inode}` : file.path;
          if (!logicalMedia.has(logicalKey)) logicalMedia.set(logicalKey, Number(file.sizeBytes) || 0);
        }
        if (!file.exists || !file.identityKnown) continue;
        const key = `${file.device}:${file.inode}`;
        let group = groups.get(key);
        if (!group) {
          group = { size: Number(file.sizeBytes) || 0, links: Number(file.links) || 0, known: /* @__PURE__ */ new Set(), selected: /* @__PURE__ */ new Set(), facts: [] };
          groups.set(key, group);
        }
        group.links = Math.max(group.links, Number(file.links) || 0);
        group.known.add(file.path);
        group.facts.push(file);
        if (selected) group.selected.add(file.path);
      }
      let reclaimed = 0;
      const blockers = /* @__PURE__ */ new Map();
      for (const group of groups.values()) {
        if (group.links > 0 && group.selected.size >= group.links) reclaimed += group.size;
        const selectedMedia = group.facts.some((file) => file.owner === "media" && file.selected);
        if (selectedMedia && group.selected.size < group.links) {
          for (const file of group.facts) {
            if (file.owner === "torrent" && !file.selected) blockers.set(String(file.ownerKey).toLowerCase(), this.model.torrentStatuses?.[String(file.ownerKey).toLowerCase()] || "UNASSOCIATED");
          }
        }
      }
      if (this.hasReclaimedTarget) this.reclaimedTarget.textContent = formatBytes(reclaimed);
      if (this.hasSelectedSummaryTarget) {
        const count = selectedFacts.filter((file) => file.owner === "media").length;
        const bytes = [...logicalMedia.values()].reduce((sum, size) => sum + size, 0);
        this.selectedSummaryTarget.textContent = count ? `${count} file${count === 1 ? "" : "s"} selected \xB7 ${formatBytes(bytes)}` : "";
      }
      this.element.querySelectorAll("[data-physical-owner]").forEach((row) => {
        const name = row.dataset.actionName;
        const value = row.dataset.actionValue;
        const state = row.querySelector("[data-physical-action-state]");
        if (!state || !name) return;
        state.textContent = actions.has(`${name}\0${value}`) ? row.dataset.actionLabel : "Preserved";
      });
      this.renderGuidance(blockers, reclaimed, selectedFacts.filter((file) => file.owner === "media").length);
      this.syncExecutionInputs(actions);
      if (this.hasSubmitButtonTarget) this.submitButtonTarget.disabled = actions.size === 0 || this.busy;
    }
    renderGuidance(blockers, reclaimed, selectedFiles) {
      if (!this.hasGuidanceTarget) return;
      if (blockers.size === 0) {
        this.guidanceTarget.hidden = true;
        return;
      }
      let fileName = this.model.mediaType === "movie" ? "movie file" : this.model.mediaType === "series" ? "episode file" : "library file";
      const plural = selectedFiles !== 1;
      if (plural) fileName += "s";
      const possessive = plural ? "their" : "its";
      this.guidanceTitleTarget.textContent = reclaimed === 0 ? `Removing the selected ${fileName} alone will not reclaim storage space.` : `Removing the selected ${fileName} alone will not reclaim all of ${possessive} storage space.`;
      const allCurrent = [...blockers.values()].every((status) => status === "CURRENT" || status === "ASSOCIATED");
      if (blockers.size === 1 && allCurrent) this.guidanceActionTarget.textContent = `${plural ? "They are" : "It is"} hardlinked to the current torrent. Also select that torrent below to reclaim the shared data.`;
      else if (allCurrent) this.guidanceActionTarget.textContent = "They are hardlinked to current torrents. Also select those torrents below to reclaim the shared data.";
      else this.guidanceActionTarget.textContent = "They are hardlinked to related torrents. Also select those torrents below to reclaim the shared data.";
      this.guidanceTarget.hidden = false;
    }
    syncExecutionInputs(actions) {
      if (!this.hasGeneratedInputsTarget) return;
      this.generatedInputsTarget.replaceChildren();
      for (const key of actions) {
        const split = key.indexOf("\0");
        const input = document.createElement("input");
        input.type = "hidden";
        input.name = key.slice(0, split);
        input.value = key.slice(split + 1);
        input.dataset.generatedInput = "";
        this.generatedInputsTarget.append(input);
      }
    }
    async submit(event) {
      event.preventDefault();
      if (this.busy || this.submitButtonTarget.disabled) return;
      this.busy = true;
      this.submitButtonTarget.disabled = true;
      this.submitButtonTarget.textContent = this.model.dryRun ? "Simulating\u2026" : "Queueing\u2026";
      if (this.hasErrorTarget) this.errorTarget.hidden = true;
      try {
        const response = await fetch(this.executeTarget.action, {
          method: "POST",
          body: new FormData(this.executeTarget),
          headers: { "Accept": "application/json", "X-Stewarr-Overlay": "1" }
        });
        const responseText = await response.text();
        let body = {};
        try {
          body = JSON.parse(responseText);
        } catch (_) {
          body = { error: responseText };
        }
        if (!response.ok) throw new Error(body.error || body.message || `status ${response.status}`);
        const label = this.element.querySelector("#removal-title")?.nextElementSibling?.textContent?.trim() || "Removal";
        this.cancelAfterAcceptance();
        announce(`${label} queued for removal.`);
        document.dispatchEvent(new CustomEvent("stewarr:mutation-accepted", { detail: body }));
      } catch (error) {
        this.busy = false;
        this.submitButtonTarget.textContent = this.model.dryRun ? "Simulate" : "Remove selected";
        this.calculate();
        if (this.hasErrorTarget) {
          this.errorTarget.textContent = error.message;
          this.errorTarget.hidden = false;
        }
      }
    }
    cancelAfterAcceptance() {
      const root = document.getElementById("modal-root");
      root?.replaceChildren();
      document.body.classList.remove("modal-open");
    }
  };
  RemovalController.targets = ["selection", "execute", "generatedInputs", "reclaimed", "selectedSummary", "guidance", "guidanceTitle", "guidanceAction", "warnings", "submitButton", "error"];

  // internal/httpui/static/src/app.ts
  var application = window.Stimulus.Application.start();
  application.register("revisions", RevisionsController);
  application.register("updates", UpdatesController);
  application.register("dashboard", DashboardController);
  application.register("shell", ShellController);
  application.register("removal", RemovalController);
})();
