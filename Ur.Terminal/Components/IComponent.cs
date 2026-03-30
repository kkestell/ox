using Ur.Terminal.Input;

namespace Ur.Terminal.Components;

public interface IComponent
{
    void Render(Core.Buffer buffer, Core.Rect area);
    bool HandleKey(KeyEvent key);
}
