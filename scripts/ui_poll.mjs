// Poll read-only page conditions across a navigation; never repeat an action.
// A page exception (for example document.body being null while the next
// document loads) is retried and reported only if the condition never holds.
export async function pollCondition(evaluate, expression, attempts = 80, wait = () => new Promise(resolve => setTimeout(resolve, 100))) {
  let lastException = '';
  for (let i = 0; i < attempts; i++) {
    try {
      if (await evaluate(expression)) return;
    } catch (error) {
      if (error.pageException) lastException = error.pageException;
      else if (!error.contextChanged) throw error;
    }
    await wait();
  }
  throw Error('page condition timed out' + (lastException ? ': ' + lastException : ''));
}
