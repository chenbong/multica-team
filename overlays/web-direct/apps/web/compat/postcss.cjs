const postcss = require('postcss');
const values = require('postcss-value-parser');

// This condition is also used by runtime.js. No user-agent sniffing.
const MODERN = '(color: oklch(0.5 0.1 120)) and (color: color-mix(in srgb, red, blue)) and (height: 100dvh) and (translate: 1px) and selector(:has(*))';

function argumentsOf(value) {
  const result = [], nodes = values(value).nodes;
  let part = '';
  for (const node of nodes) {
    if (node.type === 'space') { if (part) result.push(part); part = ''; }
    else part += values.stringify(node);
  }
  if (part) result.push(part);
  return result;
}

function legacyPrimitives(root) {
  const defaults = postcss.rule({selector: ':where(*, ::before, ::after)'});
  const inherited = postcss.rule({selector: ':root'});
  root.walkAtRules('property', rule => {
    const initial = rule.nodes.find(d => d.prop === 'initial-value');
    const inherits = rule.nodes.find(d => d.prop === 'inherits');
    if (initial) (inherits && inherits.value === 'true' ? inherited : defaults).append({prop: rule.params, value: initial.value});
    rule.remove();
  });
  // Defaults must be in the lowest layer, not unlayered (which would outrank utilities).
  const resetLayer = postcss.atRule({name:'layer', params:'properties'});
  if(defaults.nodes.length) resetLayer.append(defaults);
  if(inherited.nodes.length) resetLayer.append(inherited);
  root.prepend(resetLayer);
  root.walkDecls(d => { d.value = d.value.replace(/\b([0-9.]+)[dsl]v([hwb])/g, '$1v$2'); });
  root.walkRules(rule => {
    let independent = false, transform = false;
    rule.walkDecls(d => {
      if (d.parent !== rule) return;
      if (['translate','rotate','scale'].includes(d.prop)) {
        independent = true;
        const name = d.prop;
        const args = argumentsOf(d.value);
        if (d.value === 'none') d.value = name === 'scale' ? '1' : name === 'rotate' ? '0deg' : '0px';
        else if (name !== 'rotate') d.value = args.join(', ');
        // Independent 3D rotation: "x 45deg" -> rotate3d(1,0,0,45deg).
        if (name === 'rotate' && args.length > 1) {
          const axes = {x:'1,0,0',y:'0,1,0',z:'0,0,1'};
          d.value = 'rotate3d('+(axes[args[0]] || args.slice(0,-1).join(','))+','+args[args.length-1]+')';
        } else d.value = name+(args.length === 3 && name !== 'rotate' ? '3d' : '')+'('+d.value+')';
        d.prop = '--mc-'+name;
      } else if(d.prop === 'transform') {
        transform = true; d.prop = '--mc-transform';
        if(d.value === 'none') d.value = 'translate(0)';
      }
    });
    if(independent || transform) rule.append({prop:'transform', value:'var(--mc-translate, translate(0)) var(--mc-rotate, rotate(0deg)) var(--mc-scale, scale(1)) var(--mc-transform, translate(0))', important:rule.nodes.some(d=>d.important)});
  });
  // Custom properties ordinarily inherit; independent transforms do not.
  defaults.append({prop:'--mc-translate',value:'translate(0)'},{prop:'--mc-rotate',value:'rotate(0deg)'},{prop:'--mc-scale',value:'scale(1)'},{prop:'--mc-transform',value:'translate(0)'});
  if(!defaults.parent) resetLayer.append(defaults);
}

// Tailwind's fallback for color-mix(var(--token) 5%, transparent) is the
// opaque token. Generate inherited RGB channels for each light/dark token so
// transparency works without hard-coding the theme or changing element opacity.
function legacyAlphaColors(root) {
  const mix=/color-mix\(in (?:oklab|srgb|lab),\s*var\((--[\w-]+)\)\s+([\d.]+)%,\s*transparent\)/g;
  root.walkDecls(d=>{
    // Route CSS may reference a token declared only in the root stylesheet.
    // Export channels for every color token, not just local color-mix users.
    if(!d.prop.startsWith('--') || d.prop.endsWith('-mc-rgb'))return;
    let channels;
    const alias=d.value.match(/^var\((--[\w-]+)\)$/);
    const hex=d.value.match(/^#([a-f\d]{3}|[a-f\d]{6})$/i);
    const rgb=d.value.match(/^rgba?\(\s*([\d.]+)[, ]+([\d.]+)[, ]+([\d.]+)/);
    if(alias)channels='var('+alias[1]+'-mc-rgb)';
    else if(hex){let h=hex[1];if(h.length===3)h=h.split('').map(x=>x+x).join('');channels=[0,2,4].map(i=>parseInt(h.slice(i,i+2),16)).join(',');}
    else if(rgb)channels=rgb.slice(1,4).join(',');
    else if(d.value==='white')channels='255,255,255';
    else if(d.value==='black')channels='0,0,0';
    if(channels)d.cloneAfter({prop:d.prop+'-mc-rgb',value:channels});
  });
  root.walkDecls(d=>{d.value=d.value.replace(mix,(full,token,percent)=>'rgba(var('+token+'-mc-rgb), '+Number(percent)/100+')');});
  root.walkAtRules('supports',rule=>{
    if(!/^\(color:\s*color-mix\(/.test(rule.params))return;
    let unsupported=false;rule.walkDecls(d=>{if(/color-mix\(|oklch\(|oklab\(/.test(d.value))unsupported=true;});
    if(!unsupported)rule.replaceWith(rule.nodes);
  });
}

module.exports = function () {
  return {
    postcssPlugin: 'multica-feature-gated-compat',
    async OnceExit(root, {result}) {
      const modern = root.clone();
      const legacy = root.clone();
      legacyPrimitives(legacy);
      const {default: presetEnv} = await import('postcss-preset-env');
      const converted = await postcss([presetEnv({
        browsers:['Chrome 97'], stage:3, preserve:false,
        features:{'cascade-layers':true,'has-pseudo-class':true,
          'oklab-function':true,'color-function':true,'color-mix-function':true,
          'relative-color-syntax':true,'nesting-rules':true},
      })]).process(legacy, {from:result.opts.from, map:false});
      legacyAlphaColors(converted.root);
      root.removeAll();
      // Charset/import must remain top-level; imported application CSS was resolved by Tailwind.
      modern.nodes.filter(n=>n.type==='atrule'&&['charset','import'].includes(n.name)).forEach(n=>{root.append(n.clone());n.remove();});
      converted.root.nodes.filter(n=>n.type==='atrule'&&['charset','import'].includes(n.name)).forEach(n=>n.remove());
      const modernBranch=postcss.atRule({name:'supports',params:MODERN});
      modernBranch.append(modern.nodes);
      modernBranch.append(postcss.rule({selector:':root',nodes:[postcss.decl({prop:'--multica-compat-mode',value:'modern'})]}));
      const legacyBranch=postcss.atRule({name:'supports',params:'not ('+MODERN+')'});
      legacyBranch.append(converted.root.nodes);
      legacyBranch.append(postcss.rule({selector:':root',nodes:[postcss.decl({prop:'--multica-compat-mode',value:'legacy'})]}));
      root.append(modernBranch,legacyBranch);
    },
  };
};
module.exports.postcss = true;
module.exports.MODERN = MODERN;
