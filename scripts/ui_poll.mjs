// Poll read-only page conditions across a navigation; never repeat an action.
export async function pollCondition(evaluate, expression, attempts = 80, wait = () => new Promise(resolve => setTimeout(resolve, 100))) {
  for (let i = 0; i < attempts; i++) {
    try {
      if (await evaluate(expression)) return;
    } catch (error) {
      if (!error.contextChanged) throw error;
    }
    await wait();
  }
  throw Error('page condition timed out');
}
