import {hash, smooth} from './ascii-kit.js';
export const meta={name:'静海 · 着陆',note:'橙白色火箭从星空外缓缓下降至月面，星光闪烁，行星轻移，尾焰扬起尘雾。',cols:120,rows:34,cell:2,fps:20,ground:'#080e24',palette:['#172b50','#385477','#6f98b8','#b6d9df','#e9e7d7','#b9534c','#f19872','#a775aa','#d4ae83','#78644f','#fff2bd','#75a6be']};
export default function make(){return(t,{color})=>{let out=[];const flight=Math.max(0,t-3),p=Math.min(1,flight/18),j=p>.75&&p<1?Math.sin((p-.75)/.25*Math.PI):0,cy=-65+143*(1-(1-p)**2)+j*.7*Math.sin(t*19),cx=122+j*1.3*Math.sin(t*15);for(let r=0;r<34;r++){let line='';for(let c=0;c<120;c++){let bits=0,col=0;for(let dy=0;dy<4;dy++)for(let dx=0;dx<2;dx++){const x=c*2+dx,y=(r*4+dy)*1.2;let on=false,k=0;
if(hash(c,r)> .969&&y<112){const twinkle=hash(c+91,r+37)>.92;on=true;k=twinkle?(Math.sin(t*1.1+hash(c,r)*20)>.55?3:1):2;}
for(const [px,py,rad,cl] of [[35,48,21,5],[200,20,8,11],[195,78,11,7],[70,19,6,11]]){let xx=x-px-Math.sin(t*.18+px)*1.1,yy=(y-py)/1.2;let d=Math.hypot(xx,yy);if(d<rad){on=hash(x,y)>.24;k=cl;if(Math.hypot(xx+rad*.35,yy+rad*.3)<rad*.25)k=0;}if(px===35&&Math.abs(yy*.8+xx*.35)<1.8&&Math.abs(xx)<40){on=true;k=8;}}
for(let m=0;m<2;m++){const clock=(t+m*8)%19,age=clock-5;if(age>=0&&age<1.35){const headX=65+m*82+age*40,headY=8+age*15;const trail=(headX-x)/40;const distance=Math.abs(y-(headY-trail*15));if(trail>=0&&trail<.48&&distance<.7&&y<115){on=true;k=trail<.07?4:trail<.22?3:1;}}}
let ground=140+4*Math.sin(x*.026)+2*Math.sin(x*.1);if(y>ground){on=hash(x,y)>.67;k=9;for(const a of [35,189])if(Math.abs((x-a)**2/500+(y-152)**2/24-1)<.15){on=true;k=8;}}
let X=x-cx,Y=y-cy;let width=Y<15?Math.sqrt(Math.max(0,1-((Y-15)/15)**2))*11:11-(Y-15)*.035;if(Y>=0&&Y<51&&Math.abs(X)<width){on=true;k=(Math.floor(Y/13)+Math.floor((X+12)/11))%2?6:4;if(Y<12)k=6;let d=Math.hypot(X,Y-24);if(d<7){k=d>5?6:d>4?4:0;}}
if(Y>40&&Y<62&&Math.abs(X)>8&&Math.abs(X)<19-(Y-40)*.25){on=true;k=5;}if(Y>50&&Y<57&&Math.abs(X)<7){on=true;k=9;}
if(p<1&&Y>57&&Y<57+(1-p)*24+7&&Math.abs(X)<(7-(Y-57)*.19)*( .8+.2*Math.sin(t*22+y))){on=true;k=Y<65?10:11;}
if(t>16&&t<28&&y>127&&y<147&&Math.abs(X)<(t-16)*5+8){let d=Math.sin(x*.14-t*2)+Math.sin(y*.5+t);if(d> .4&&hash(x,y)>.52){on=true;k=3;}}
if(on && (t>=3 || hash(c,r+731)<Math.max(0,(t-.2)/2.8))){bits|=1<<[[0,3],[1,4],[2,5],[6,7]][dy][dx];col=k;}}
line+=String.fromCharCode(0x2800+bits);color[r*120+c]=col;}out.push(line);}return out.join('\n');};}
