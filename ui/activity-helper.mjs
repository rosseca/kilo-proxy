// Format JSON by whitespace only: preserve numeric precision, duplicate keys,
// escaping and key order. SSE and truncated JSON remain verbatim.
export function formatTraceJSON(source) {
  try { JSON.parse(source); } catch { return source; }
  let result='', depth=0, quoted=false, escaped=false, previous='';
  const newline=()=>{result+='\n'+'  '.repeat(depth);};
  for(const char of source){
    if(depth>40 || result.length>1048576)return source;
    if(quoted){result+=char;if(escaped)escaped=false;else if(char==='\\')escaped=true;else if(char==='"')quoted=false;continue;}
    if(/\s/.test(char))continue;
    if(char==='"'){quoted=true;result+=char;}
    else if(char==='{' || char==='['){result+=char;depth++;newline();}
    else if(char==='}' || char===']'){
      depth--;
      if(previous==='{' || previous==='[')result=result.trimEnd();else newline();
      result+=char;
    }else if(char===','){result+=char;newline();}
    else if(char===':')result+=': ';
    else result+=char;
    previous=char;
  }
  return result;
}
